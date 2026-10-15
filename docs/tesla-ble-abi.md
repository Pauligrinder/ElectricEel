# Tesla BLE C ABI

Public header: `helper/session/abi/tesla_ble.h`.

The production Harbour application uses the private Go library through the Rust
wrapper. ABI v2 covers operations, observations and diagnostics. The former
standalone daemon and its legacy transport have been removed.

## Current operations

- Explicit connection with VCSEC or infotainment authentication.
- Explicit disconnection.
- VCSEC body-controller state (protobuf-derived JSON with defaults emitted).
- Navigation to typed GPS coordinates or destination text.
- Start/stop fresh advertisement observation with expiring opaque target tokens.
- Enable/disable the independent passive-entry authentication responder.
- Submit enrollment and verify requested-key enrollment/authentication.
- Execute the full BLE command catalogue with typed integer/enum, floating-point
  and byte parameters. Fleet-only `rename-key`/`product-info` report unsupported.
- Read signer public-point bytes for key-identity verification without Bluetooth.

There is no automatic connection on a query, idle timer, proximity state machine,
key-file I/O, presence controller or application reconnect schedule in the new
library. Rust supplies a VIN, optional adapter identifier and a 32-byte big-endian
P-256 private scalar. Go copies and validates those inputs before create returns.

Rust's `protocol_client.rs` owns the controller/retry/idle threads and pairing
orchestration. `phone_key.rs` holds pure proximity/inside policy;
`protocol_commands.rs` is the single command-argument validator (ranges, arity,
units) and builds the typed operations; `keys.rs` owns
RustCrypto P-256 and compatible SEC1/PKCS8 persistence. Go's passive responder
attaches before authentication and is independent of foreground operations.

## Async and lifetime contract

- Create performs no Bluetooth I/O and emits no callbacks.
- Handles identify sessions; requests use nonzero, strictly increasing IDs.
- Submit copies all input bytes and returns immediately with admission status.
- One operation is admitted at a time. Rust owns higher-level queues and policy.
- Rejected submissions do not emit completion; accepted operations emit one
  terminal completion under normal/recoverable execution.
- Completion can race submit return. Register Rust pending state before calling C.
- Callbacks are serialized per session and run on Go-managed threads. They copy
  data into Rust channels; they do not perform file I/O, call Qt, block or re-enter
  the library. Buffers are borrowed only until the callback returns.
- Operation results distinguish cancellation, timeout, not-connected, Bluetooth
  off, authentication failure (including `ErrKeyNotPaired` and inactive-key
  faults), internal bugs and uncertain success. Radio-off and adapter errors are
  permanent and not retried. A command on a dropped link reports not-connected
  and never reconnects silently. Cancellation cannot undo a sent vehicle
  command.
- Dropping the Rust operation future requests cancellation. The completion still
  drains even if that future's receiver has gone away.
- Stop is asynchronous and idempotent. It rejects new work and cancels active work.
- Destroy requires stop first and blocks until backend teardown and return from
  the stopped callback. Only then may Rust free the callback context.
- A stuck teardown or teardown panic does not fabricate a successful stopped
  barrier. Ownership remains alive; this failure needs diagnostics and repair.
- Successfully loaded Go libraries stay loaded for the application's lifetime.
  The Rust loader is unsafe because callers must trust both code and ABI signatures.

Rust's `Session::shutdown().await` should run on a backend worker before Drop.
Drop performs the final destruction barrier and can block; it must not be used on
the GUI thread as a substitute for explicit asynchronous shutdown.

## Fault handling

Recoverable operation panics produce a diagnostic with panic value, Go stack,
operation and request ID, then an internal-bug completion and session stop. Rust
does not accept further operations on faulted state. Teardown panics are logged
without claiming resources were released. This does not contain fatal Go runtime
errors or all dependency-owned goroutine panics.

Each session opens BlueZ with its own diagnostics logger (`bluez.Open(log)`);
there is no process-global Go logger, and Go writes no log files. Diagnostics
arrive as observations of kind 6 on a bounded Rust channel. Rust maintenance
writes them to the daily logger; overflow is counted/reported. Logger detachment waits for
borrowed callbacks before releasing their context.

Advertisements coalesce. Other raw observation overflow faults the session
rather than silently losing an authoritative transition. Derived phone-key
snapshots coalesce by kind; stopped state has a separate priority slot. Command
completions and stop/destroy ownership barriers are never lossy queues.

## Host verification

CI runs these on every push (`ffi` job in `.github/workflows/ci.yml`). From the
repository root, with any scratch directory:

```sh
out="$(mktemp -d)"
CC=gcc GOEXPERIMENT=cgocheck2 bash helper/session/build-library.sh "$out/libteslable.so"
TESLA_BLE_TEST_LIBRARY="$out/libteslable.so" \
  cargo test --locked --manifest-path helper/Cargo.toml --lib --features runtime -- --include-ignored
cargo clippy --locked --manifest-path helper/Cargo.toml --lib --features app-entry -- -D warnings
```

From `helper/session` (same `$out`):

```sh
CC=gcc go test -race ./...
gcc -Wall -Wextra -Werror -I abi abi/smoke.c \
  -L "$out" -lteslable -Wl,-rpath,"$out" \
  -pthread -o "$out/teslable-smoke"
"$out/teslable-smoke"
```

The C smoke test and Rust integration test call the real Go library without
Bluetooth. They verify configuration rejection, typed outcomes, ID handling,
copied inputs and stop/destroy behaviour. Go race tests additionally exercise
cancellation, submit/stop races, panic diagnostics and callback-return barriers.

## Sailfish target proof

On 2026-10-13 the library was cross-built using the locally available
`coderus/sailfishos-platform-sdk-aarch64` image and the
`SailfishOS-5.2.0.15-aarch64` target. The real C smoke executable passed under
Scratchbox2's target execution environment.

The Qt runtime integration test also passes under SDK target execution: the real
Rust entrypoint, Qt adapter and Go runtime coexist, Go callbacks return typed
public-key data and shutdown crosses the callback-return barrier. This is not a
substitute for a real-device Sailjail/Bluetooth field pass.

Use host Go outside sb2 with its C compiler wrapped by sb2:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
  CC='sb2 -t SailfishOS-5.2.0.15-aarch64 gcc' \
  go build -buildmode=c-shared -o /home/mersdk/build/libteslable.so ./cmd/teslable
```

The source, Go installation/module cache, build output and `TMPDIR` must be
accessible under paths visible to both the host tools and sb2 (paths under
`/home/mersdk` worked). Running the host Go executable itself inside sb2 did not.
Read-only checkouts mounted with a different owner need `-buildvcs=false`;
both `helper/build-go-library-sdk.sh` and `helper/session/build-library.sh`
build with `-buildvcs=false -trimpath`.

ELF inspection found AArch64 shared-object output, `NODELETE`, dependencies on
`libc.so.6` and `libresolv.so.2`, and required GLIBC versions through 2.34. That
proves compatibility with the SDK used for this check, not every older Sailfish
release. The moderator's 5.1 target and actual device/Sailjail behaviour still
require a field pass. The rebuilt RPM passes all Harbour sections on SDK 5.2.

## Application packaging

`helper/make-app-bundle.sh` stages Rust and invokes `helper/build-go-library-sdk.sh`
to cross-build/strip Go with the SDK compiler. qmake installs only
`lib/libteslable.so`, so stale `app/bin` products cannot ship. The RPM strips both
ELF files and installs the private library as 0644. Narrow private-library metadata
exclusions retain allowed Qt/system dependencies; explicit Qt package requirements
and versioned runtime requirements are removed.

Hardware verification and outstanding field checks are tracked in
[ble-field-test.md](ble-field-test.md) and [limitations.md](limitations.md).
