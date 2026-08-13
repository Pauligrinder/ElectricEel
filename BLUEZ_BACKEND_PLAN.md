# BlueZ D-Bus BLE backend for ElectricEel — working plan & status

## Purpose of this file

This is the canonical, durable record of the plan to move the app's BLE
transport from go-ble/raw-HCI (which takes exclusive control of the phone's
radio and drops other connections, e.g. a soundbar) to a cooperative
`org.bluez` D-Bus backend — and, as an end-state, to remove the two-package
app/helper split (the Rust helper moves in-process into the app via a C ABI).

**If a session is interrupted, re-read this file first.** The Status
Dashboard below plus the phase checklists tell exactly what is done and what
to pick up next. Append entries to the Completion Log (with today's date)
when you finish a step; flip `[x]` boxes as you go.

Position in the repo: a plan/working document, not shipped code. Relevant code
lives under `helper/session/` (Go), `helper/src/` (Rust — daemon today, to be
kept as the in-process app core), `app/` (Silica UI). Context: README.md,
KNOWN_ISSUES.md.

---

## Status dashboard (2026-08-13)

```
Phase 0  plan written & agreed          [x] DONE
Phase 1  bluez package + unit tests     [x] DONE
Phase 2  wire into session main.go      [~] CODE DONE — needs hardware validation
Phase 3  keygen/pair in Go              [~] CODE DONE — needs hardware validation
Phase 4  remove the split               [ ] NOT STARTED  (needs Phase 2 verified)
Phase 5  bake in (default bluez)        [ ] NOT STARTED
```

Next action: **on-device validation** — build `tesla-session` for the device,
run with `-ble-backend=bluez`, and verify scan→connect→session→round-trips,
the soundbar survival test (Section 7), plus the Phase 3 GenerateKey→Pair
flow. Phase 2 and 3 code is complete and unit-tested in this repo; only
real-hardware checks remain.

Status: `go test -race ./...`, `go vet ./...`, and
`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` all pass in
`helper/session/` as of 2026-08-13.

---

## 1. Goal

Make the app's BLE transport go through `org.bluez` (the cooperative
Sailfish/BlueZ stack) instead of `go-ble`'s raw HCI capture, so other radio
users (soundbar) are never disturbed — and make the two-package split
removable.

Non-goals: no protocol/crypto changes, no upstream fork, no behavior change
to commands.

## 2. Design decision: plug in at the connector seam, no fork

The transport only touches two upstream functions in
`helper/session/main.go:114,117`: `ble.InitAdapterWithID` + `ble.NewConnection`.
Everything downstream (`vehicle.NewVehicle`, vendored `execute()`, pairing)
consumes a plain `connector.Connector` interface
(`pkg/connector/connector.go`). That interface is our seam:

```
session main.go ──> vehicle.NewVehicle(conn Connector, ...) ──> execute() (vendored commands)
                         ▲
                         └── today: ble.Connection (raw HCI)
                             new:   bluez.Connection (org.bluez D-Bus)   ← we implement this
```

Implement a `connector.Connector` over BlueZ D-Bus in a NEW package
`helper/session/bluez`, reusing only exported API (`ble.VehicleLocalName`,
`protocol.LoadPrivateKey`) plus already-vendored `commands_vendor.go`. No
upstream source is modified — the backend is chosen where we build the
connection, not inside the library. Matches how the repo already vendor-pins.

## 3. Target architecture

| | Today | End-state |
|---|---|---|
| BLE transport | `tesla-control` raw HCI (CAP_NET_ADMIN, setcap) | `tesla-session --ble-backend=bluez` (D-Bus) |
| Privileged parts | `teslacontrold` systemd service, setcap, system bus auth | none |
| Executor | Rust helper (systemd service) execs Go `tesla-session` over D-Bus from the app | Rust core **linked in-process** into the app (C ABI via cbindgen); app spawns one Go `tesla-session` child (JSON-over-stdio, existing protocol) |
| Packages | `harbour-teslacontrol` + `teslacontrold` | one Harbor app (Rust static lib + Go binary bundled in the app RPM) |
| Sandbox | helper must be unsandboxed | app + child run in Sailjail with stock `Bluetooth` permission (org.bluez D-Bus); no cross-process authorization |
| C++ responsibility | D-Bus calls + (post-Phase-4 plan) all session logic in `teslaclient.cpp` | thin QObject→C shim only (strings/bools); all logic in Rust in-process |

---

## 4. New component: `helper/session/bluez`

### 4.1 Public surface (mirror of what session main.go uses today)

```go
package bluez

type ScanResult struct {
    Address   string
    LocalName string
    RSSI      int16
}

// Scan returns after finding the vehicle beacon (or ctx expires).
func Scan(ctx context.Context, conn *dbus.Conn, adapterID string, vin string) (*ScanResult, error)

// Connect scans (if needed), connects, subscribes to the RX characteristic,
// and returns a live connector.
func Connect(ctx context.Context, conn *dbus.Conn, adapterID string, vin string, target *ScanResult) (connector.Connector, error)

// NewDbus opens the system bus via org.freedesktop.DBus.   // reused across Scan/Connect
```

`Connect` returns a `connection` struct implementing `connector.Connector`
with the exact upstream semantics:
- `Receive() <-chan []byte` (buffered 5), `Send(ctx, []byte)`, `VIN()`,
  `Close()` (idempotent)
- `PreferredAuthMethod() = connector.AuthMethodGCM`,
  `RetryInterval() = 1s`, `AllowedLatency() = 4s`
  (same constants as upstream `ble.go:29-30`)

### 4.2 org.bluez call sequence

Discovery:
1. `ObjectManager.GetManagedObjects` → find adapter `/org/bluez/hciX`
   implementing `org.bluez.Adapter1` (match requested id; default = first).
2. Ensure `Powered=true` (`Properties.Set` on Adapter1) — normal under
   bluetoothd, only set if off.
3. `Adapter1.SetDiscoveryFilter({"Transport":"le","DuplicateData":false})`,
   then `StartDiscovery`.
4. Subscribe to `PropertiesChanged`; watch `/org/bluez/hciX/dev_*`
   `org.bluez.Device1` objects; match `Name == ble.VehicleLocalName(vin)`
   (name format: literally `VehicleLocalName`, exported `ble.go:133-137`).
   Capture `Address`, `RSSI`.
5. `Adapter1.StopDiscovery`.

Connect / GATT:
6. `Device1.Connect`, await `ServicesResolved=true` (PropertiesChanged signal).
7. `ObjectManager.GetManagedObjects` under `dev_*` → find `GattService1` with
   `UUID == 00000211-b2d1-43f0-9b88-960cebf8b91e`; then two
   `GattCharacteristic1`: `00000212…` (=TX) and `00000213…` (=RX).
8. RX: `StartNotify` on RX char; feed the `Value` property of each
   `PropertiesChanged` into the connector's inbox (thread-safe channel send,
   drop when full — same as upstream `flush`/buffer handling).
9. TX: serialize via a mutex; split into `blockLength` byte chunks like
   `Send()` (`ble.go:108-127`); `WriteValue(value, "request")` per chunk
   (confirm "request" vs "command": `WriteCharacteristic(.., false)` upstream
   = with-response → "request").
10. MTU: use negotiated ATT MTU if BlueZ exposes it (verify exact property
    during implementation; some versions expose on the characteristic); else
    assume `maxBLEMessageSize` like upstream and fall back to
    `DefaultMTU-3` (17) on write error — reproduce `ble.go:349-356`.
11. `Close()` → `StopNotify` + `Device1.Disconnect`; idempotent.

### 4.3 D-Bus plumbing choices
- Use `github.com/godbus/dbus` (already a transitive dep, pinned
  `4481cbc300e2`) — ZERO new dependencies, consistent with the repo's
  minimal-deps style.
- Hide all `dbus.BusObject`/property-signal marshaling behind small internal
  interfaces (`adapter`, `btDevice`, `gattCharacteristic`) so unit tests can
  inject fakes (see Testing). Optional upgrade path to
  `altdesktop/go-bluez/v5` if GATT dict handling gets tedious — rejected for
  now to avoid dependency growth.

### 4.4 Error mapping
- Adapter missing / power refused → `IsAdapterError`-style error, distinct
  message for UI.
- `Device1.Connect` "connection deferred not supported" / "br-connection-…"
  → map to scan/connect failures; keep upstream's `ErrMaxConnectionsExceeded`
  semantics where practical (BlueZ has no `Connectable` property, so we
  approximate and document it — the real signal is a failed `Connect`).
- `context deadline` surfaces identically to today, so UI error text doesn't
  change.

### 4.5 Pairing & keygen
- Pairing is the vendored `add-key-request` command run through `execute()`
  (pairing currently execs `tesla-control add-key-request …`,
  `helper/src/helper.rs:322-330`) — runs over the same Connector, so it
  inherits the BlueZ backend for free once it runs through session `main.go`.
- Keygen (currently `tesla-keygen` exec) moves into Go:
  `authentication.NewECDHPrivateKey(rand.Reader)` +
  `protocol.SavePrivateKey`/`LoadPrivateKey` → add a `keygen` request type to
  the session protocol. No raw HCI involved; pure crypto.

---

## 5. Integration into `helper/session/main.go`

- Add `-ble-backend=hci|bluez` (default `hci` until hardware-verified).
- `ensureConnectedLocked` (`main.go:101-139`): if `bluez` → open one
  `dbus.Conn`, `bluez.Scan` (if no cached target) then `bluez.Connect` →
  `vehicle.NewVehicle(conn, skey, nil)`; never call `ble.InitAdapterWithID`
  (that's what does the HCIDEVDOWN/UP/down + user-channel capture in
  `socket.go:92-108`).
- Session reuse, 90s idle timer, JSON protocol: unchanged.
- **No-fallback rule:** while the Rust helper still exists,
  `session_client.rs` must NOT exec one-shot `tesla-control` when the session
  path errors in bluez mode — a fallback would reintroduce raw HCI. In the
  end-state there is no `tesla-control` binary at all, so the rule is
  structural.

## 6. Packaging / phase-out of the split

Phase-gated (Section 9). Final state — the Rust core moves from an
unsandboxed systemd service to an **in-process static library** inside the
app, reached through a tiny cbindgen-generated C ABI; Go `tesla-session`
remains the transport child it already is:

- `helper/` becomes a Rust library crate (`crate-type = ["staticlib"]`, plus
  a small `#[no_mangle] extern "C"` surface: an opaque `Core*` handle with
  `core_new/core_run_command/core_generate_key/core_pair/core_get_status/
  core_free`, all string/bool payloads — no complex marshaling). Keep
  `session_client.rs`, `config.rs`, and the Phase 2/3 orchestration (no-fallback
  rule, keygen/pair routing) verbatim; drop only the zbus interface, the
  system-bus connection, and `authorize.rs` (no cross-process boundary → no
  credential checks needed).
- `cbindgen` generates the header consumed by `app/src/teslaclient.cpp`, which
  shrinks to a QObject wrapper that owns the worker thread (the Rust core
  drives the Go child from that thread; results cross back via a Qt signal /
  guarded accessor pumped on a timer — the one non-trivial C++ bit, kept as
  small as possible).
- Build: cross-compile the Rust static lib for armv7hl/aarch64 in the app's
  RPM build (cargo target dir alongside the SDK toolchains) and link it in;
  bundle the Go `tesla-session` binary in the app RPM and spawn it as a child
  on first use.
- Key file + config live in the app's data dir (reachable inside Sailjail; no
  `StateDirectory`, no cross-UID `authorize()`).
- Delete: `systemd/teslacontrold.service`, `TeslaControlHelper.permission`,
  the `setcap`/CAP_NET_ADMIN dance, the entire KNOWN_ISSUES NoNewPrivs/
  ProtectSystem saga (lines 445-492). The Rust *code* is not deleted — it is
  retained as the in-process core (this is the deliberate deviation from the
  earlier "port session_client.rs to C++" plan; see Phase 4 and Completion Log).
- `harbour-teslacontrol.desktop` keeps `Permissions=Bluetooth` — that already
  grants sandboxed D-Bus access to `org.bluez`. `TeslaControlHelper`
  permission file is deleted.

---

## 7. Acceptance criteria (especially Phase 2)

- **Soundbar survival test:** stream audio to a soundbar, run a full command
  set, assert the stream survives and no HCI device down/up happens (`hcitool
  dev` state unchanged); repeat with session idle-teardown cycling.
- Full command + pairing flows succeed on a real car over `-ble-backend=bluez`.
- No regression in command catalog/output (existing
  `test_command_catalog_matches_upstream` + `test_qml_catalog_matches_upstream`
  still pass).

## 8. Testing strategy

Unit (in `helper/session/bluez`, no hardware needed):
- Scan matching: fake `btDevice` emitting names → correct match;
  `VehicleLocalName` correctness (assert against upstream `ble.go:133-137`).
- TX framing: buffer split at `blockLength`, chunk reassembly, MTU-fallback
  replica of `ble.go:349-356`.
- RX inbox: `PropertiesChanged.Value` → ordered datagrams; overflow drops
  without blocking.
- `Close()` idempotency, Send/Receive thread-safety (`go test -race`).

Integration (on-device, the real gate):
- Real vehicle: scan → connect → session → `lock`/`climate` round-trips;
  pairing `add-key-request` flow.
- Soundbar acceptance test (Section 7).
- Persistence: 90s idle teardown → reconnect works; backgrounding/killing the
  app releases the car link cleanly.

## 9. Risks & mitigations

| Risk | Mitigation |
|---|---|
| BlueZ scan/connect timing slower than raw HCI (go-ble uses 10ms active scan; car advertises 20–150ms) | Default BlueZ discovery catches it; verify on-car. Keep `hci` fallback flag until proven |
| Connection-interval / MTU negotiated by BlueZ differs | Out of our control but same as CoreBluetooth path (works upstream on macOS); MTU fallback replicates upstream |
| `Connectable` semantics differ (no BlueZ property) | Map failed `Connect` → `ErrMaxConnectionsExceeded`-style error; document divergence |
| One-shot `tesla-control` fallback reintroducing raw HCI | No-fallback rule in bluez mode (Section 5); binary removed in phase 4 |
| Sailjail child-process exec blocked | Bundle binary in app data path; verify inside jail before phase 4 |
| Rust-in-app build: cross-compiling the Rust static lib for armv7hl/aarch64 inside the app RPM build (toolchain/linker flags, target dir plumbing) | Model on the Go cross-build already proven in CI; build the lib with cargo + a target-specific `CARGO_BUILD_TARGET` in the spec `%build`, add a `%prep` stage that compiles lib+header; verify early in phase 4 before touching `teslaclient.cpp` |
| C ABI ↔ C++ threading (blocking Rust call must not stall QML) | Keep the Rust core on one worker thread; the C++ side is a thin wrapper with a result polled via QTimer / signal — no cross-thread ownership of the Go child |

---

## 10. Work breakdown

### Phase 0 — plan (DONE 2026-08-13)
- [x] Diagnose root cause (raw HCI takeover breaks soundbar)
- [x] Confirm backend-plug seam (connector.Connector), no-fork approach
- [x] Write this plan + status tracking

### Phase 1 — `bluez` package (no hardware needed) — DONE 2026-08-13
- [x] Scaffold `helper/session/bluez/` (go.mod already covers deps; add
      `github.com/godbus/dbus` to `helper/session/go.mod` as direct dep if it
      becomes one) — **done**, godbus promoted from indirect to direct by
      `go mod tidy`, pinned `v0.0.0-20190726142602-4481cbc300e2`
- [x] Internal interfaces + fakes: `adapter`, `btDevice`, `gattCharacteristic`,
      `dbusConnector` (to make D-Bus swappable in tests)
      — implemented as `dbusBus`/`dbusCaller` + `fakeBluez`/`fakeCaller`
      (method-call dispatch by name + scriptable device/model + signal pump)
- [x] `VehicleLocalName` compatibility test (assert == upstream format)
      — `TestVehicleBeaconNameMatchesUpstream` (uses `ble.VehicleLocalName`
      directly, guards drift)
- [x] `Scan()`: GetManagedObjects → adapter lookup → Power → SetDiscoveryFilter
      + StartDiscovery → Device1.Name match → StopDiscovery
      — `scan.go`; polling GetManagedObjects every 100ms (deterministic,
      testable) instead of signal-based discovery (documented in file header)
- [x] `Connect()`: Device1.Connect → ServicesResolved → GATT discovery →
      Subscribe RX (StartNotify + PropertiesChanged.Value → inbox)
      — `gatt.go` + `connection.go` rxLoop
- [x] `Send()` with blockLength chunking + MTU fallback (17)
      — `connection.go Send/writeChunk`; fallback to `defaultMTU-3` on write
      error; copies datagrams out of the RX backing array to avoid aliasing
- [x] `connection` implementing `connector.Connector` (Receive/Send/VIN/Close,
      auth method/retry/latency constants) — `connection.go`
- [x] `Close()` idempotent (StopNotify + Disconnect + RemoveMatch)
- [x] Error mapping (IsAdapterError-style, ErrMaxConnectionsExceeded
      approximation)
      — rudimentary: all paths wrap with `bluez: ...` context so errors read
      cleanly; upstream's `IsAdapterError` equivalence and the
      `ErrMaxConnectionsExceeded` approximation are deferred to Phase 2 where
      the session layer decides on error text (marked NOT fully done here)
- [x] Unit tests green: `go test -race ./...` from `helper/session/`
      — also `go vet ./...` clean, `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`
      cross-build clean

**Deliverable:** `helper/session/bluez/` with fake-backed unit tests passing.

Public API refinements vs. the original plan (see Completion Log):
- `bluez.Open()` returns a `*Conn` sharing ONE D-Bus signal registration
  across Scan/Connect (avoids godbus channel-registration leak); exported
  `Scan/Connect` became methods on `*Conn`.
- `ScanResult.Address` (MAC) replaced by `ScanResult.Path` (org.bluez
  Device1 object path — the identifier BlueZ's Connect needs).
- The pinned godbus (2019) has no `Variant.Store`; decode via
  `Variant.Value()` through small `variantBool/String/Bytes/Int16` helpers.

### Phase 2 — wire into session main.go, on-car validation (CODE DONE, NEEDS HARDWARE)
- [x] `-ble-backend=hci|bluez` (default hci) — flag in `main.go`, plumbed
      through `session_client.rs` spawn args; `TESLACONTROLD_BLE_BACKEND`
      env read once in `main.rs` (single source of truth, validated hci/bluez)
- [x] `ensureConnectedLocked` bluez branch (never call InitAdapterWithID)
      — holds one `bluez.Conn` on `s.bluez` for the process lifetime (closed
      in a new `closeBluezLocked`, called from `main()`'s exit paths)
- [x] No-fallback rule in `session_client.rs` while Rust helper still exists
      — bluez mode returns `HelperError::SessionUnavailable` instead of
      exec'ing a raw-HCI `tesla-control`; `Pair()` also refuses to run over
      raw HCI in bluez mode (clear error, not silent teardown)
- [x] Connect retry semantics matching upstream
      `NewConnectionFromScanResult` — `connect`/`tryConnect` in `gatt.go`
      retry transient scan/link failures until ctx expires; `IsAdapterError`
      classifies permanent adapter/radio failures (operation not permitted,
      NotReady/NotPowered, ServiceUnknown, no adapter) so they surface
      immediately; fake-backed tests for retry + error classification
- [ ] On-device: scan→connect→session→round-trips on real car
- [~] On-device: soundbar survival — scan half verified, command half needs
      a car. 2026-08-13 on the Sailfish phone (192.168.1.121): ran the
      session under `-ble-backend=bluez` while the soundbar was streaming
      A2DP; adapter `Powered=true` and `Discovering=true` (StartDiscovery
      actually runs), soundbar `Connected=true` + MediaTransport1
      `State=active` stayed up across all samples, session exited cleanly
      with the no-car `context deadline exceeded`. Fixing the root-cause
      hang (adapter property typo `Power`→`Powered` + missing retry
      backoff in `connect()`) is what made the scan path reachable at all.

**Deliverable:** backend toggle works on a real car without dropping the
soundbar stream.

### Phase 3 — keygen/pair in Go (CODE DONE, NEEDS HARDWARE)
- [x] `keygen` request type — `dispatchKeygen` in `main.go` (pure crypto,
      no BLE; generates via `ecdsa.GenerateKey(P256)` + scalar →
      `protocol.UnmarshalECDHPrivateKey` + `protocol.SavePrivateKey`,
      avoiding the unimportable `internal/authentication`; returns the PEM
      public key on Stdout; `-f` arg = force-overwrite, mirroring
      tesla-keygen `create` semantics)
- [x] `add-key-request` runs through session path (inherits bluez transport)
      — `Helper::pair` now routes through `SessionClient::run("add-key-request", …)`
      when a session exists (both backends); replaces the Phase 2 bluez Pair
      guard, which is now obsolete (bluez pairing works via the session)
- [x] `tesla-keygen` / one-shot `tesla-control` no longer executed in bluez
      mode — `GenerateKey` routes through `SessionClient::keygen` when a
      session exists; one-shot fallbacks (`generate_key_one_shot`,
      `run_binary`) remain only for no-session / hci + session-error paths
- [ ] Verify: GenerateKey then Pair against a real car

**Deliverable:** pairing and keygen fully in Go, no privileged binaries.

### Phase 4 — remove the split: Rust core in-process (CONDITIONAL on Phase 2 verified)
- [ ] Convert `helper/` to a staticlib crate; add `#[no_mangle] extern "C"`
      surface (opaque `Core` handle, `core_new/run_command/generate_key/
      pair/get_status/free`, string+bool payloads); generate the header with
      cbindgen; keep `session_client.rs`/`config.rs`/orchestration untouched
- [ ] `app/src/teslaclient.cpp` → thin QObject wrapper: owns the worker thread,
      calls the C ABI, publishes results to QML (signal or QTimer-polled
      accessor); delete the D-Bus client path
- [ ] Cross-compile the Rust static lib for armv7hl/aarch64 and link it into
      `app/rpm/harbour-teslacontrol.spec` (`%build` cargo step); bundle the Go
      `tesla-session` binary in the app RPM; spawn as child from the Rust core
- [ ] Delete `systemd/teslacontrold.service`, `TeslaControlHelper.permission`
- [ ] Remove setcap/CAP_NET_ADMIN/no-new-privs content from README/KNOWN_ISSUES
- [ ] Single RPM install-from-scratch test on-device (pair, control, soundbar
      immune, no devel-su)

**Deliverable:** one Harbor-compatible app (Rust static lib + Go child); no
helper package, no systemd service.

### Phase 5 — bake in
- [ ] Default `-ble-backend=bluez`; keep `hci` as documented escape hatch
- [ ] Rewrite README architecture section; add end-state KNOWN_ISSUES entry
- [ ] Final `go test -race ./...` + `cargo test`/clippy (now the in-process lib)
      clean

**Deliverable:** BlueZ is the shipped default; docs current.

---

## Completion log (append newest last)

- 2026-08-13 — Plan written and agreed in session (this file). Root cause of
  soundbar disconnect confirmed: go-ble `socket.open()` does
  HCIDEVDOWN/UP/DOWN + binds HCI_CHANNEL_USER (exclusive), so the adapter is
  taken from bluetoothd for the whole process; no raw-HCI patch can coexist,
  hence the BlueZ D-Bus transport swap. Phase 0 complete.
- 2026-08-13 — **Phase 1 DONE.** Implemented `helper/session/bluez/`:
  `bluez.go` (Conn/Open + `dbusBus`/`dbusCaller` seam + godbus adapters +
  variant decode helpers), `scan.go` (adapter lookup, power-on, LE discovery
  filter, StartDiscovery, beacon-name match via GetManagedObjects polling),
  `gatt.go` (Device1.Connect, ServicesResolved poll, GATT service/char walk,
  UUID dash-normalization), `connection.go` (connector.Connector: length-prefix
  framing, blockLength chunking with MTU-23 fallback, PropertiesChanged
  notification loop, idempotent Close). Tests via `fakeBluez` in
  `fake_test.go`; `go test -race ./...` green, `go vet` clean, aarch64
  cross-build clean. Refinements recorded in the Phase 1 checklist: polling
  discovery (not signals), `ScanResult.Path` (not Address), `Conn`-level signal
  reuse, godbus-2019 variant decoding. Error-mapping equivalence
  (IsAdapterError / ErrMaxConnectionsExceeded) deferred to Phase 2.
- 2026-08-13 — **Phase 2 code DONE** (hardware validation outstanding).
  `main.go`: `-ble-backend` flag; session gains `bleBackend` + `bluez`
  fields and a `conn` typed `connector.Connector`; `ensureConnectedLocked`
  branches bluez (lazy `bluez.Open()` + `s.bluez.Connect`, never
  `InitAdapterWithID`) vs hci (unchanged); `closeBluezLocked` called from
  both `main()` exit paths (signal handler + normal return). `bluez/gatt.go`:
  `connect` split into `connect`/`tryConnect` with upstream-equivalent retry
  on transient failures; `IsAdapterError` added (permanent adapter/radio
  failures surface immediately). Rust side: `session_client.rs` carries a
  `ble_backend` passed as `-ble-backend` at spawn; `main.rs` reads
  `TESLACONTROLD_BLE_BACKEND` (default `hci`, validated); `helper.rs`
  `run()` no-fallback rule in bluez mode (returns
  `HelperError::SessionUnavailable` instead of exec'ing raw-HCI
  tesla-control) and `Pair()` refuses raw HCI in bluez mode. New
  `gatt_retry_test.go` covers retry + IsAdapterError. Green: `go test -race
  ./...` + `go vet`, `cargo test` (12) + clippy. Remaining: on-device
  round-trips + soundbar acceptance (Sections 7-8).
- 2026-08-13 — **Phase 3 code DONE** (hardware validation outstanding).
  `main.go`: new `dispatchKeygen` request type (pure crypto, no BLE) that
  generates a P256 key via `ecdsa.GenerateKey` + `protocol.UnmarshalECDHPrivateKey`
  + `protocol.SavePrivateKey` (keeps `internal/authentication` out of the
  module), re-prints an existing key's public half without `-f` (mirrors
  tesla-keygen `create`), and returns the PEM on Stdout; `dispatch` intercepts
  `cmd == "keygen"` before any connection work. Rust: `SessionClient::keygen`
  request method; `Helper::generate_key` routes through it when a session
  exists (falling back to a new `generate_key_one_shot` helper for
  no-session/hci paths); `Helper::pair` routes `add-key-request` through the
  session when one exists (inheriting bluez transport — the obsolete Phase 2
  bluez Pair guard is removed). New `main_test.go` tests: keygen creates a
  loadable PEM key with 0600 perms, force/no-force semantics, corrupt-key
  recovery. Green: `go test -race` + `go vet` + aarch64 cross-build, `cargo
  test` + `cargo clippy --all-targets -- -D warnings`. Remaining: GenerateKey
  then Pair on a real car (Phase 3 acceptance).
- 2026-08-13 — **Phase 4 RE-SCOPED: Rust stays, in-process.** Abandoned the
  original plan to delete the Rust helper and port `session_client.rs` to
  C++/QML (removes `teslaclient.cpp` D-Bus path). Now: `helper/` becomes a
  staticlib linked into the app, called through a tiny cbindgen C ABI (opaque
  `Core` handle, string/bool payloads) from a thin QObject wrapper on a worker
  thread. Go `tesla-session` remains the spawned child (JSON-over-stdio).
  Why: it deletes the split (systemd service, setcap, D-Bus, authorize) while
  keeping all the battle-tested Rust logic — the app stays one package, and
  only the system-bus/authorize.rs surface (not the session/transport logic)
  is removed. The one real cost is cross-compiling the Rust lib into the app
  RPM (toolchain), not the C ABI itself; de-risked in Section 9. Docs/risks/
  phases updated to match.

## One-time verification commands

- `cd helper/session && go test -race ./...`
- `cd helper && cargo test && cargo clippy --all-targets -- -D warnings` (continues to apply once helper becomes the in-process static lib)