// Build with: CGO_ENABLED=1 go build -buildmode=c-shared ./cmd/teslable
package main

/*
#cgo CFLAGS: -I${SRCDIR}/../../abi
#include "tesla_ble.h"
#include <stdlib.h>
static void ee_deliver(ee_callback cb, void *ctx, ee_message *msg) { cb(ctx, msg); }
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"sync"
	"time"
	"unsafe"

	"electric-eel-session/library"
)

var registry = struct {
	sync.Mutex
	next  uint64
	items map[uint64]*entry
}{items: make(map[uint64]*entry)}

type entry struct {
	session    *library.Session
	stopping   bool
	destroying bool
}

func recoverABI(status *C.uint32_t, handle uint64) {
	if value := recover(); value != nil {
		*status = C.EE_INTERNAL_BUG
		message := fmt.Sprintf("C ABI bridge panic=%v\n%s", value, debug.Stack())
		_, _ = fmt.Fprintln(os.Stderr, message)
		registry.Lock()
		item := registry.items[handle]
		registry.Unlock()
		if item != nil {
			item.session.Fault(message)
		}
	}
}

type sink struct {
	handle   uint64
	callback C.ee_callback
	context  unsafe.Pointer // Opaque C/Rust-owned pointer; never dereferenced by Go.
}

func cBytes(bytes []byte) C.ee_bytes {
	if len(bytes) == 0 {
		return C.ee_bytes{}
	}
	return C.ee_bytes{data: (*C.uint8_t)(C.CBytes(bytes)), len: C.uint64_t(len(bytes))}
}

func (s *sink) deliver(kind uint32, id uint64, result library.Result) {
	// Message and all referenced buffers are C allocated; Go retains none of
	// these pointers after the callback. This also works with strict cgo checks.
	message := (*C.ee_message)(C.calloc(1, C.size_t(C.sizeof_ee_message)))
	defer C.free(unsafe.Pointer(message))
	message.size, message.kind = C.sizeof_ee_message, C.uint32_t(kind)
	message.session, message.request = C.uint64_t(s.handle), C.uint64_t(id)
	message.status = C.uint32_t(result.Status)
	message.payload, message.error = cBytes(result.Payload), cBytes([]byte(result.Error))
	defer C.free(unsafe.Pointer(message.payload.data))
	defer C.free(unsafe.Pointer(message.error.data))
	C.ee_deliver(s.callback, s.context, message)
}

func (s *sink) Complete(id uint64, result library.Result) { s.deliver(C.EE_COMPLETE, id, result) }
func (s *sink) Diagnostic(message string) {
	s.deliver(C.EE_DIAGNOSTIC, 0, library.Result{Status: library.InternalBug, Error: message})
}
func (s *sink) Stopped() { s.deliver(C.EE_STOPPED, 0, library.Result{}) }

func (s *sink) Observe(event library.Observation) {
	message := (*C.ee_message)(C.calloc(1, C.size_t(C.sizeof_ee_message)))
	defer C.free(unsafe.Pointer(message))
	message.size, message.kind = C.sizeof_ee_message, C.EE_OBSERVATION
	message.session = C.uint64_t(s.handle)
	message.status, message.event = C.uint32_t(event.Status), C.uint32_t(event.Kind)
	message.rssi, message.target, message.value = C.int32_t(event.RSSI), C.uint64_t(event.Target), C.uint32_t(event.Value)
	message.error = cBytes([]byte(event.Error))
	defer C.free(unsafe.Pointer(message.error.data))
	C.ee_deliver(s.callback, s.context, message)
}

func bytes(view C.ee_bytes, limit uint64) ([]byte, bool) {
	if uint64(view.len) > limit || (view.len != 0 && view.data == nil) {
		return nil, false
	}
	if view.len == 0 {
		return nil, true
	}
	return C.GoBytes(unsafe.Pointer(view.data), C.int(view.len)), true
}

//export ee_abi_version
func ee_abi_version() C.uint32_t { return 2 }

//export ee_session_create
func ee_session_create(config *C.ee_config, callback C.ee_callback, context unsafe.Pointer, handle *C.uint64_t) (status C.uint32_t) {
	defer recoverABI(&status, 0)
	if handle == nil {
		return C.EE_INVALID
	}
	*handle = 0
	if config == nil || config.size != C.sizeof_ee_config || config.version != 2 || callback == nil {
		return C.EE_INVALID
	}
	vin, ok := bytes(config.vin, 17)
	if !ok {
		return C.EE_INVALID
	}
	adapter, ok := bytes(config.adapter, 128)
	if !ok {
		return C.EE_INVALID
	}
	key, ok := bytes(config.private_scalar, 32)
	if !ok {
		return C.EE_INVALID
	}
	backend, err := library.NewTesla(string(vin), string(adapter), key)
	clear(key)
	if err != nil {
		return C.EE_INVALID
	}
	registry.Lock()
	defer registry.Unlock()
	registry.next++
	if registry.next == 0 {
		backend.Close()
		return C.EE_INTERNAL_BUG
	}
	id := registry.next
	registry.items[id] = &entry{session: library.New(backend, &sink{handle: id, callback: callback, context: context})}
	*handle = C.uint64_t(id)
	return C.EE_OK
}

//export ee_session_submit
func ee_session_submit(handle C.uint64_t, request *C.ee_request) (status C.uint32_t) {
	defer recoverABI(&status, uint64(handle))
	if request == nil || request.size != C.sizeof_ee_request || request.reserved != 0 || request.timeout_ms == 0 || uint64(request.timeout_ms) > uint64(math.MaxInt64/int64(time.Millisecond)) {
		return C.EE_INVALID
	}
	text, ok := bytes(request.text, 8000)
	if !ok {
		return C.EE_INVALID
	}
	if request.count > 16 || (request.count != 0 && request.values == nil) {
		return C.EE_INVALID
	}
	values := make([]library.Value, int(request.count))
	for i, value := range unsafe.Slice(request.values, int(request.count)) {
		if value.reserved != 0 || value.kind > 3 {
			return C.EE_INVALID
		}
		data, ok := bytes(value.bytes, 8192)
		if !ok {
			return C.EE_INVALID
		}
		values[i] = library.Value{Kind: uint32(value.kind), Integer: uint64(value.integer), Number: float64(value.number), Bytes: data}
	}
	registry.Lock()
	defer registry.Unlock()
	item := registry.items[uint64(handle)]
	if item == nil {
		return C.EE_INVALID_HANDLE
	}
	return C.uint32_t(item.session.Submit(library.Request{ID: uint64(request.id), Operation: library.Operation(request.operation), Domain: uint32(request.domain), Timeout: time.Duration(request.timeout_ms) * time.Millisecond, Latitude: float64(request.latitude), Longitude: float64(request.longitude), Text: string(text), Target: uint64(request.target), Command: uint32(request.command), Values: values}))
}

//export ee_session_cancel
func ee_session_cancel(handle C.uint64_t, request C.uint64_t) (status C.uint32_t) {
	defer recoverABI(&status, uint64(handle))
	registry.Lock()
	defer registry.Unlock()
	item := registry.items[uint64(handle)]
	if item == nil {
		return C.EE_INVALID_HANDLE
	}
	return C.uint32_t(item.session.Cancel(uint64(request)))
}

//export ee_session_stop
func ee_session_stop(handle C.uint64_t) (status C.uint32_t) {
	defer recoverABI(&status, uint64(handle))
	registry.Lock()
	defer registry.Unlock()
	item := registry.items[uint64(handle)]
	if item == nil {
		return C.EE_INVALID_HANDLE
	}
	item.stopping = true
	item.session.Stop()
	return C.EE_OK
}

//export ee_session_destroy
func ee_session_destroy(handle C.uint64_t) (status C.uint32_t) {
	defer recoverABI(&status, uint64(handle))
	registry.Lock()
	item := registry.items[uint64(handle)]
	if item == nil {
		registry.Unlock()
		return C.EE_INVALID_HANDLE
	}
	if !item.stopping {
		registry.Unlock()
		return C.EE_INVALID
	}
	if item.destroying {
		registry.Unlock()
		return C.EE_BUSY
	}
	item.destroying = true
	registry.Unlock()
	item.session.Join()
	registry.Lock()
	delete(registry.items, uint64(handle))
	registry.Unlock()
	return C.EE_OK
}

func main() {}
