# Tesla BLE C ABI

Public header: `helper/session/abi/tesla_ble.h`.

The production Harbour application uses the private Go library through the Rust
wrapper. ABI v2 covers operations, observations and diagnostics. The standalone
daemon retains its legacy transport separately, excluded from the app build.

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
`protocol_commands.rs` parses input, schedules and key paths; `keys.rs` owns
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
- Operation results distinguish cancellation, timeout, not-connected, internal
  bugs and uncertain success. Cancellation cannot undo a sent vehicle command.
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

Go/BlueZ diagnostics use a bounded Rust channel. Rust maintenance writes them
to the daily logger; overflow is counted/reported. Logger detachment waits for
borrowed callbacks before releasing their context.

Advertisements coalesce. Other raw observation overflow faults the session
rather than silently losing an authoritative transition. Derived phone-key
snapshots coalesce by kind; stopped state has a separate priority slot. Command
completions and stop/destroy ownership barriers are never lossy queues.

## Host verification

From the repository root (choose an existing temporary output directory):

```sh
CC=gcc GOEXPERIMENT=cgocheck2 bash helper/session/build-library.sh /tmp/opencode/libteslable.so
TESLA_BLE_TEST_LIBRARY=/tmp/opencode/libteslable.so \
  cargo test --manifest-path helper/Cargo.toml --lib --features runtime -- --include-ignored
cargo test --manifest-path helper/Cargo.toml --lib --features runtime
cargo clippy --manifest-path helper/Cargo.toml --lib --features app-entry -- -D warnings
```

From `helper/session`:

```sh
CC=gcc go test -race ./...
gcc -Wall -Wextra -Werror -I abi abi/smoke.c \
  -L /tmp/opencode -lteslable -Wl,-rpath,/tmp/opencode \
  -pthread -o /tmp/opencode/teslable-smoke
/tmp/opencode/teslable-smoke
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
Read-only checkouts mounted with a different owner may need `-buildvcs=false`
for this isolated proof build; ordinary release builds should retain VCS metadata.

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

See `docs/go-c-abi-migration-plan.md` for verification and outstanding field checks.
