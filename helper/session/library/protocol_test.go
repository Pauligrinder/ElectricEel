package library

import (
	"electric-eel-session/internal/phoneprotocol"
	"github.com/teslamotors/vehicle-command/pkg/protocol/protobuf/universalmessage"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCommandIDsMatchRustAndEverySchemaRejectsWrongTypes(t *testing.T) {
	if len(commandNames) != len(commandSchema) {
		t.Fatalf("names=%d schemas=%d", len(commandNames), len(commandSchema))
	}
	data, err := os.ReadFile("../../src/commands.rs")
	if err != nil {
		t.Fatal(err)
	}
	catalog := strings.SplitN(strings.SplitN(string(data), "pub(crate) const COMMAND_CATALOG", 2)[1], "];", 2)[0]
	names := regexp.MustCompile(`"([a-z0-9-]+)"`).FindAllStringSubmatch(catalog, -1)
	if len(names) != len(commandNames) {
		t.Fatalf("Rust=%d Go=%d", len(names), len(commandNames))
	}
	for index, name := range commandNames {
		if names[index][1] != name {
			t.Fatalf("ID %d: Rust=%s Go=%s", index+1, names[index][1], name)
		}
		values := make([]Value, len(commandSchema[index]))
		for i := range values {
			values[i].Kind = 99
		}
		_, err := validateCommand(Request{Command: uint32(index + 1), Values: values})
		if len(values) > 0 && err == nil {
			t.Fatalf("%s accepted invalid ABI types", name)
		}
	}
}

func TestTypedParametersCannotWrapOrTransmitNonFiniteValues(t *testing.T) {
	for _, request := range []Request{
		{Command: 19, Values: []Value{{Kind: 1, Integer: math.MaxUint64}}},
		{Command: 8, Values: []Value{{Kind: 2, Number: math.NaN()}}},
		{Command: 27, Values: []Value{{Kind: 2, Number: 100}}},
	} {
		if _, err := validateCommand(request); err == nil {
			t.Fatal(request)
		}
	}
}

func TestPassiveEntryRejectsIdentificationAndAcceptsBareAndRoutableRequests(t *testing.T) {
	for _, level := range []uint64{0, 1, 2, 99} {
		inner := protowire.AppendTag(nil, 3, protowire.VarintType)
		inner = protowire.AppendVarint(inner, level)
		bare := protowire.AppendTag(nil, 3, protowire.BytesType)
		bare = protowire.AppendBytes(bare, inner)
		routed, err := proto.Marshal(&universalmessage.RoutableMessage{Payload: &universalmessage.RoutableMessage_ProtobufMessageAsBytes{ProtobufMessageAsBytes: bare}})
		if err != nil {
			t.Fatal(err)
		}
		for _, data := range [][]byte{bare, routed} {
			got, ok := authenticationLevel(data)
			want := level == 1 || level == 2
			if ok != want || (ok && got != int(level)) {
				t.Fatalf("level=%d got=%d valid=%v", level, got, ok)
			}
		}
	}
	for _, data := range [][]byte{nil, {0xff}, {0x1a, 0xff}, {0x1a, 0x02, 0x18, 0xff}, {0x1a, 0x00}} {
		if _, ok := authenticationLevel(data); ok {
			t.Fatalf("malformed/identification accepted: %x", data)
		}
	}
}

func TestPassiveEntrySharesTokenValidationWithLegacyDispatcher(t *testing.T) {
	for _, token := range [][]byte{{0x0a, 0x01, 0x42}, {0x0a, 0xff}} {
		request := protowire.AppendTag(nil, 2, protowire.BytesType)
		request = protowire.AppendBytes(request, token)
		request = protowire.AppendTag(request, 3, protowire.VarintType)
		request = protowire.AppendVarint(request, 2)
		bare := protowire.AppendTag(nil, 3, protowire.BytesType)
		bare = protowire.AppendBytes(bare, request)
		legacy, legacyOK := phoneprotocol.ParseAuthenticationRequest(bare)
		level, libraryOK := authenticationLevel(bare)
		want := len(token) == 3
		if legacyOK != want || libraryOK != want || (want && level != legacy.RequestedLevel) {
			t.Fatalf("token %x: legacy=%v library=%v", token, legacyOK, libraryOK)
		}
	}
}

func TestBackgroundPanicLogsStackAndStopsActualBackend(t *testing.T) {
	key := make([]byte, 32)
	key[31] = 1
	backend, err := NewTesla("5YJ3E1EA7KF000001", "", key)
	if err != nil {
		t.Fatal(err)
	}
	sink := newSink()
	session := New(backend, sink)
	go func() { defer backend.backgroundGuard(); panic("injected background protocol bug") }()
	message := receive(t, sink.diagnostics)
	if !strings.Contains(message, "injected background protocol bug") || !strings.Contains(message, "goroutine") {
		t.Fatal(message)
	}
	receive(t, session.done)
	if session.Submit(Request{ID: 1, Operation: PublicKey, Timeout: time.Second}) != Stopping {
		t.Fatal("faulted session reused")
	}
}
