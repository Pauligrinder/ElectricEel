# Production review — resolved

The five defects and four lower-cost improvements identified by the production
review have been addressed; two of them concerned the socket transport between
the app and a session child process, which has since been removed entirely
(marked *obsolete* below). Regression tests remain enabled in CI. The review
covered Rust core/runtime and persistence, Go session/BlueZ transport, the
vendored dispatcher, the Qt notification adapter, dashboard/settings flows,
and existing test coverage.

## Defects and fixes

### 1. P1: Reused phone-key links skipped infotainment authentication

Presence authenticates VCSEC only. Previously, ordinary commands returned nil
from `sessionDomains`, which meant all domains on a fresh link but skipped
authentication entirely on an existing link. The dispatcher cannot lazily
authenticate a missing domain, so climate, charging, media and navigation failed
until a dashboard query happened to establish infotainment authentication.

**Fixed:** the protocol library selects the command's actual domain on both
fresh and reused links (`helper/session/internal/phoneprotocol` `CommandDomain`,
applied in `helper/session/library`). Lock/unlock, BLE wake, keys,
trunk/frunk, tonneau, body-controller state and passive entry stay VCSEC-only;
infotainment commands authenticate infotainment; enrollment and session-info
need no handshake. Ready domains reuse their existing session.

**Coverage:** `TestCommandDomainsMatchVehicleHandlers`
(`helper/session/library/domain_test.go`) checks the domain of every ABI
command.

### 2. P1: Dashboard telemetry survived a VIN change

The dashboard replaced its VIN but retained the previous car's lock, climate,
window, battery and freshness values. Toggles could therefore choose commands
for the new car using the old car's state.

**Fixed:** `app/qml/pages/FirstPage.qml` clears telemetry, optimistic snapshots
and pending status state when the VIN changes. Every status request has a unique
page/sequence/VIN identifier. Late or duplicate replies cannot update telemetry,
restore an old optimistic snapshot, or advance a newer refresh chain.

**Coverage:** `production_vehicle_switch_test.cjs` and `firstpage_status_test.cjs`
exercise VIN switching, stale success/failure replies, duplicate replies and
the complete serial refresh chain.

### 3. P2: Partial BLE writes left the framing stream reusable

After part of a length-prefixed datagram was transmitted, an immediate new
message could be appended into the unfinished frame.

**Fixed:** `helper/session/bluez/connection.go` retires uncertain or partially
written streams, notifies `Dropped`, and rejects further sends. MTU fallback
is restricted to explicit write-length rejection. Errors preserve the underlying
deadline/transport cause and the possibility that the command had an effect;
uncertain sends are not automatically retransmitted.

**Coverage:** `TestProductionReviewPartialSendRetiresFramingStream`, the MTU
fallback test and the deadline test cover stream retirement, successful safe
fallback, and uncertain timeout classification.

### 4. P2: Fatal outbound parent-transport errors did not stop serving (obsolete)

Response/event writers of the session child's parent socket only logged
failure. The fix (closing the failed transport and shutting down the server)
was superseded when the session child, its socket transport and its tests were
removed: the app now calls the Go library in process through the C ABI
([tesla-ble-abi.md](tesla-ble-abi.md)).

### 5. P2: Failed configuration loads emitted synthetic successful data

Queue admission failure emitted blank `configLoaded` data. Settings accepted it
and enabled Save, allowing a saved VIN/enrollment state to be erased.

**Fixed:** The Qt adapter emits `configLoadError` and exposes the state as the
`configReady`/`configError` properties. Settings keeps its fields,
disables Save, ends the loading indicator and provides a retry action. The
dashboard reports the failure without replacing its known configuration.

**Coverage:** `configload_test.cpp` uses the actual Qt adapter with a rejecting
runtime; `configload_review_test.cjs` exercises the Settings error handler.
The Qt regression is included in the existing CI QML job.

## Additional improvements

- **Bounded frames (obsolete):** the 1 MiB frame limit applied to the
  newline-delimited socket transport, which has been removed together with the
  session child; the C ABI passes typed, copied buffers instead.
- **Behavioral tests:** Source-token assertions in `production_review_test.cjs`
  were replaced with executions of the actual handlers. Tests verify unique
  IDs, display/send precision, replacement shares, double-send prevention and
  both vehicle/transport toggle failures without requiring specific source
  tokens such as `Math.random` or `toFixed`.
- **Logging:** Rust is the only writer of the daily log file; Go/BlueZ
  diagnostics reach it through queued callbacks. Rust caches its daily append
  handle, prunes only on rollover or directory change, and caps the file at
  10 MiB, holding an advisory `flock` around size checks and appends so
  concurrent app instances cannot exceed the cap. Existing data is retained;
  diagnostics still reach stderr when the daily file is full.
- **Freshness:** VehicleState records per-category timestamps. The aggregate
  timestamp is the oldest retained category, so a fresh lock reply cannot make
  older climate or battery data appear fresh. Partial refresh errors remain
  visible on the dashboard.

## Verification

- Rust: `cargo test --locked --all-features`, `cargo fmt --check`, and
  `cargo clippy --locked --all-targets --all-features -- -D warnings`
  (CI additionally lints the shipped `app-entry` feature set).
- Go: gofmt, `go vet ./...`, and `go test -race ./...`.
- JavaScript: `node --test app/tests/*_test.cjs`.
- Qt 5.15 in an Ubuntu 24.04 container: QML syntax lint, configuration-load
  adapter regression, and numeric argument regression. The adapter also builds
  and passes against the real generated Rust C ABI header.
- Translation catalogs regenerated after QML changes; repeated generation is
  byte-identical. The existing freshness gate will see the regenerated catalogs
  once these changes are committed.

These are hardware-free checks. Vehicle firmware and Sailfish integration still
use the qualification boundaries described in `limitations.md`.
