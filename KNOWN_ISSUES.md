# Known issues / what a "prod quality" pass could not fully close

This file tracks gaps found during a code review pass (2026-08-07) that
were either fixed, or documented here because closing them needs
something this environment doesn't have: a real Sailfish device, the
Platform SDK, or a car to pair against.

## Fixed 2026-08-13 - bluez BLE session "hung" on-device for the full connect timeout; root cause was a property typo plus a hot retry loop, not a D-Bus deadlock

Debugged on the Sailfish phone (192.168.1.121) against `org.bluez`.
`tesla-session -ble-backend=bluez` under the Section-7 soundbar survival
test would sit "hung" until the connect timeout: main goroutine blocked
in `CallWithContext`, one inWorker spinning decoding `a{oa{sa{sv}}}`
(GetManagedObjects replies), thousands of leaked godbus `createCall.func2`
watchdog goroutines (goroutine IDs ~5341), and `Properties.Get`/enumeration
coming back "context deadline exceeded". A goroutine dump at first
suggested a D-Bus reply deadlock; `dbus-monitor` looked empty but was
falling back to eavesdropping (BecomeMonitor denied) so it only saw
broadcast signals, not point-to-point calls - both red herrings.

Root cause was two compounding bugs in `helper/session/bluez/`:
- `scan.go`'s `ensurePowered` read the adapter property `"Power"` - BlueZ
  Adapter1 actually exposes `"Powered"` - so every `Properties.Get` failed
  with `No such property 'Power'` (confirmed against the live adapter via
  probe binaries).
- `Scan()` returned that error immediately (probe control succeeded), but
  `Connect()`'s retry loop in `gatt.go` had **no backoff**, so it retried
  hot until ctx deadline: hammering bluetoothd with GetManagedObjects,
  leaking a godbus call goroutine per attempt, and degrading into the
  apparent hang. This is why one-shot probes passed while the full session
  "hung" - only the session path's connect loop is hot.

Fix:
- `scan.go`: `"Power"` → `"Powered"` (get/set + error text).
- `gatt.go`: `connect()` now backs off 100ms between retries
  (`select` on `ctx.Done()` vs `time.After(100ms)`), so a transient error
  can't spin or leak goroutines.
- `fake_test.go`: the fake now serves `Powered` and returns the real
  `No such property 'Power'` error for `Power`, mirroring bluetoothd.

Verified on-device after fix: goroutine count dropped ~5341 → 73; main
sits in the beacon-poll select at `scan.go:70`; FIFO repro exits cleanly
with generic `context deadline exceeded` (no-car scan timeout being the
correct behavior); `go test ./bluez/...` passes. Also closed the Phase-2
scan half of the Section-7 soundbar survival test on-device (see
`BLUEZ_BACKEND_PLAN.md` §Phase 2): adapter `Powered/Discovering=true` and
soundbar `Connected=true` / transport `State=active` stayed up while the
session scanned, and it exited cleanly - under the old code discovery
never even started because power-on failed instantly.

## Fixed 2026-08-12 - Settings could silently wipe a configured VIN (load/save race)

Symptom: on-device, a previously-working VIN kept reverting to unset -
"No VIN configured" and no car silhouette on the front page, every time,
even right after re-entering the VIN in Settings and getting a "Saved"
confirmation. `helper`/`app` versions matched (ruled out the D-Bus
signature-skew issue below) and `config.json` genuinely had `"vin": ""`
after each of these round trips - the save itself was the thing erasing
it.

Root cause: `SettingsPage.qml`'s fields (`vinField` et al.) start at
their blank/default values and are only populated once `GetConfig`'s
async reply lands (`Component.onCompleted: teslaClient.refreshConfig()`
-> `onConfigLoaded`). The Save button had no gate on that reply having
arrived, so opening Settings and hitting Save before it did - trivially
reproducible by opening Settings while a refresh from the front page was
still in flight - submitted the fields' still-blank defaults. An empty
VIN is intentionally accepted by `validate_config`
(`helper/src/config.rs`) as "clear the VIN", so this wasn't rejected: it
silently overwrote a good VIN with an empty one. Confirmed on-device via
SSH (`devel-su cat /var/lib/teslacontrold/config.json`) showing
`vin: ""` immediately after a "Saved" round trip where the VIN field had
displayed the *placeholder* text (a realistic-looking example VIN,
`5YJ3E1EA0PF000000`) rather than real content, compounding the
confusion - the blank field was easy to mistake for a populated one.

Fix (`app/qml/pages/SettingsPage.qml`):
- Added `configReady`, false until the first real `onConfigLoaded` fires.
  Save is `enabled: page.configReady`, so it cannot fire against
  default/blank fields anymore - the race is structurally closed, not
  just narrowed.
- `statusText` starts as `"Loading current configuration..."` instead of
  blank, so the form visibly isn't ready yet rather than looking like an
  empty-but-usable state.
- The VIN field's placeholder was changed from a real-looking example VIN
  to `"17-character VIN"`, which can't be mistaken for actual saved
  content at a glance.

## Fixed 2026-08-12 - "No VIN configured" after the 0.1.6 upgrade (helper/app D-Bus signature skew)

Symptom: right after upgrading the app to 0.1.6, the front page showed
"No VIN configured" even though the VIN was saved, no car silhouette
rendered (not even after picking a model and saving from Settings), and
Settings would not confirm the saved VIN.

Root cause: 0.1.6 added a `model` field to the helper's D-Bus interface
- `SetConfig` gained one argument and `GetConfig` gained one return
value (`(ssii)` -> `(sssii)` / `ssiibs` -> `sssiibs`). The app and the
helper ship as separate RPMs (`harbour-teslacontrol` and
`teslacontrold`) that must be updated together. Qt's `QDBusPendingReply`
treats *any* reply-signature mismatch as an error and returns **empty**
arguments (verified empirically with a local D-Bus bus against a
rebuilt 0.1.5-layout helper: GotConfig -> `InvalidSignature`, `vin` =
`""`), so a stale helper left `configLoaded` never firing and the whole
config appeared unset.

Fix (`app/src/teslaclient.cpp`):
- `refreshConfig()` now parses the `GetConfig` reply by its actual
  signature and accepts **both** layouts - `sssiibs` (0.1.6+) and the
  pre-0.1.6 `ssiibs` (model assumed `""`). An app and helper a version
  apart therefore still display VIN/hasKey and render the (VIN-guessed)
  silhouette; a `qWarning` notes the mismatch.
- `setConfig()`'s error path translates the zbus/Qt signature-mismatch
  error into an actionable message ("teslacontrold is too old for this
  app version - update the helper package to 0.1.6 or later.") instead
  of the raw D-Bus text.
- Parsing uses `QDBusMessage::arguments()` rather than a
  `QDBusPendingReply` constructed from a message, so it keeps compiling
  against the older Qt the Sailfish SDK bundles.

On-device when this recurs: `systemctl status teslacontrold` +
`journalctl -u teslacontrold -b` to confirm which binary is actually
running, and `journalctl -t harbour-teslacontrol -b` for the app-side
`qWarning` above. The clean fix remains installing matching app+helper
RPMs from the same release.

**Follow-up in the same session - versions surfaced on the UI (0.1.7).**
So an app/helper mismatch can never again hide as a cryptic blank
config, the two halves now report their versions, always visible:
- The helper gained a `GetVersion` D-Bus method returning
  `CARGO_PKG_VERSION` (`helper/src/helper.rs`); an older helper answers
  `UnknownMethod`, which is exactly how the app recognizes "helper too
  old".
- The app reads its own version from `APP_VERSION` (set from `VERSION`
  in `app/harbour-teslacontrol.pro`) and queries `GetVersion` via
  `refreshHelperVersion()` (`app/src/teslaclient.cpp`).
- Settings gets an "About" section (app + helper versions, warning on
  mismatch/too-old); FirstPage's banner now fires not just for a missing
  helper but for one that reports no version or a mismatched one.
- The release workflow stamps `VERSION` (in the `.pro`) and the helper's
  `Cargo.toml` version from the same tag and asserts they're equal, so a
  matching release always reports matching versions.

## Fixed 2026-08-11 - full-codebase review: 11 findings, mostly commands that always errored

An independent review (Rust helper, Go session companion, C++/QML app,
systemd/D-Bus/RPM packaging, CI - build/clippy/tests verified, not
read-only) found that most of `CommandCatalog.js`'s arguments for
less-common commands were wrong in ways that made the command fail
every single time it was tapped from the UI, plus a few smaller
security/robustness gaps. Every finding was checked against the actual
pinned `v0.4.1` source (`helper/session/commands_vendor.go`, and for one
finding, empirically via `protojson.Format` on real generated Go types -
see below) before fixing, not taken on faith.

**Commands that always failed, now fixed** (`app/qml/js/CommandCatalog.js`
unless noted):
- Seat Heater: `SEAT` used underscores (`front_left`); upstream wants
  hyphens (`front-left`, `2nd-row-left`, ...). `LEVEL` was a 0-3 slider;
  upstream wants `off|low|medium|high`.
- Set Temperature: sent a bare number; upstream needs the unit glued on
  (`21C`). Fixed via a new `sendSuffix` arg option
  (`app/qml/pages/ArgumentDialog.qml`) distinct from the display-only
  `unit` label.
- Start Software Update: `DELAY` sent bare seconds; upstream parses it
  with Go's `time.ParseDuration`, which requires a unit (`600s`) - only
  `DELAY=0` ever worked, since that's the one value `ParseDuration`
  accepts unitless. Same `sendSuffix` fix.
- Add Charge Schedule: `TIME` placeholder implied a single time
  (`07:30`); upstream wants a `START-END` range (`22:00-06:00`).
  Preconditioning's `TIME` is genuinely a single time - the two used to
  share one arg-builder function despite having different formats; split
  into `chargeScheduleAddArgs()`/`preconditionScheduleAddArgs()`.
- Add/Remove schedule `TYPE`: was `home|work|favorite`; upstream is
  `home|work|other|id` (`favorite` was never a real value).
- Session Info `DOMAIN`: was `vehicle_security`; upstream is `vcsec`.
- Add Key `FORM_FACTOR`: was `phone_key`; upstream enum is
  `nfc_card|ios_device|android_device|cloud_key` (this app's own key
  enrolls as `cloud_key`).
- Auto Seat & Climate `POSITIONS`: was a free-text placeholder
  (`front_left,front_right`); upstream wants literal `L`, `R`, or `LR`.
- `parental-controls-*` (5 commands, `CommandCatalog.js` +
  `helper/src/commands.rs`'s `COMMAND_CATALOG`/`PIN_COMMANDS`): don't
  exist upstream at all - "parental-controls" is only a `state` CATEGORY
  name (a read-only status query), never a family of action commands.
  Removed entirely rather than left as buttons guaranteed to error.
- `rename-key`/`product-info`: `requiresFleetAPI: true` upstream, so
  they always fail with "command requires a FleetAPI OAuth token" in
  this BLE-only app. Removed from the UI catalog (left in the Rust
  allow-list - they're real, well-formed commands, just not useful to
  offer as buttons; no security reason to also drop them there).
- Also found while verifying the above (not in the original review): the
  `DAYS` placeholder text said `Mon,Tue,Wed`, but upstream's day-name
  parser only recognizes `Tues`/`Thurs`, not `Tue`/`Thu`.

**The actual bug behind "optional" schedule fields never being optional**
(`app/qml/pages/ArgumentDialog.qml`): enum and slider fields set
`argSpec.__value` in `Component.onCompleted` unconditionally, including
for fields marked `optional: true` - a `ComboBox`/`Slider` has no "nothing
selected" state to represent "leave this unset," so a schedule add
always sent `REPEAT=once` (overriding the weekly default) and `ID=0`.
Fixed two ways: optional enum fields now get a synthetic "(not set)"
choice at index 0 whose value is `""` (so the existing skip-if-empty
logic in `onAccepted` actually fires); optional int/float fields fall
back to a plain text field instead of a slider (a slider can never be
"empty," a text field naturally is unless a default is given).

**Charging-state dashboard comparison** (`app/qml/js/VehicleState.js`,
`app/qml/pages/FirstPage.qml`): the review guessed this was a
`"Charging"` vs `"CHARGING"` casing bug. Checked empirically instead of
guessing further - `ChargingState` is a protobuf `oneof` of empty
messages (`oneof type { Void Charging = 5; ... }`), not a plain enum, so
`protojson` serializes the active variant as a *nested object* keyed by
the exact field name (`{"Charging": {}}`), not a string at all. Verified
by writing a throwaway Go program against the real `v0.4.1` generated
types and calling `protojson.Format` directly, rather than trusting
protobuf's JSON-naming rules from memory. So the bug was a type mismatch
(object compared to string, always false), not a casing one - and the
string value `"Charging"` FirstPage.qml already compared against was
correct all along. Also verified the other fields this dashboard reads
(`locked`, door/window bools, temps, `isClimateOn`) do *not* share this
trap - they use proto3's single-field-oneof "explicit optional scalar"
idiom, which `protojson` flattens to a plain value, confirmed the same
way.

**Smaller fixes:**
- `SetConfig` didn't validate `key_name` at all (`helper/src/config.rs`):
  added a length cap (64 chars) and charset restriction, mirroring the
  existing VIN validation. `key_name` is functionally near-inert in this
  app (always `-keyring-type file`, so tesla-control never reaches the
  keyring-lookup code path that would use it) - this is about not
  logging/storing an unbounded or control-character string, not a
  functional risk.
- `Config::load` didn't re-validate on load, only `SetConfig`'s write
  path did. Added `Config::sanitize()`, called after every load: resets
  only the specific out-of-range field(s) to their defaults (not the
  whole config) rather than trusting a hand-edited `config.json`
  unconditionally. Low real risk (the file is 0600, owned by the
  service's own account - see the RPM spec) but cheap defense-in-depth.
- `run_binary`'s spawn-failure path (`helper/src/helper.rs`) silently
  returned empty output with no log line - the hardest failure mode to
  reproduce got zero diagnostics. Now logged via `eprintln!`.
- `Pair()` had a hardcoded 60s timeout independent of the configured
  `connect_timeout_sec` (up to 300s) baked into the same argv - could
  cut off a connect attempt still legitimately in progress. Now uses the
  same `connect + command + 10s` envelope `Run()` does, plus a flat 30s
  allowance for the physical NFC-card tap it waits for.
- `app/src/teslaclient.cpp`'s D-Bus calls relied on Qt/libdbus's default
  reply timeout (~25s) for everything, including `Run`/`Pair`, which can
  legitimately take up to that same `connect+command+10s`/`+30s`
  envelope (up to 640s at max configured timeouts) - a real command
  could get killed client-side well before the server gave up on it.
  Fixed by building a raw `QDBusMessage` and calling
  `QDBusConnection::asyncCall(msg, timeoutMs)` with an explicit long
  timeout for just those two calls, rather than raising
  `QDBusInterface`'s interface-wide timeout (which would also make
  `GetConfig`/`SetConfig`/`GenerateKey` wait just as long if the service
  itself were down). Verified with a syntax-only `g++` compile against
  real Qt6 D-Bus headers (confirms the API calls are correct; doesn't
  confirm behavior against the Sailfish SDK's actual Qt5 - not
  buildable in this environment, see the pre-existing note below).
- `let _ = &conn;` before `thread::park()` in `helper/src/main.rs` was a
  cryptic no-op to silence an unused-variable warning. Renamed the
  binding to `_conn` instead - idiomatic, same effect, self-explanatory.

**Drift-prevention test added** (`helper/src/commands.rs`): the root
cause behind most of the above is that `CommandCatalog.js`,
`COMMAND_CATALOG`, and `commands_vendor.go` are three independently
hand-maintained lists with no automatic cross-check. Added
`KNOWN_UPSTREAM_COMMANDS` (a snapshot of every command name that
actually exists in the pinned `v0.4.1` tag) plus two tests -
`test_command_catalog_matches_upstream` and
`test_qml_catalog_matches_upstream` (the latter `include_str!`s
`CommandCatalog.js` directly and regex-extracts its `cmd(...)` calls,
no Node/build-step dependency) - asserting both catalogs are subsets.
Verified these tests actually catch regressions, not just pass
vacuously: temporarily reintroduced a bogus command into each catalog,
confirmed the test failed with a clear message naming it, then reverted.

**Deliberately not changed**, per the review's own framing:
- The D-Bus allow-list security model (any app declaring
  `Permissions=TeslaControlHelper` gets full car control, no per-app
  authorization beyond the allow-list) - already documented as the
  security model's known weakest point elsewhere in this file; a
  pairing-time shared secret would close it but is a real feature, not a
  fix.
- `Run()` returning `Busy` as a D-Bus error while `Pair()` returns
  `ok=false` for the same condition - already an intentional, commented
  parity choice (matches the original implementation), not an oversight.
- `PUBLIC_KEY` args accepting arbitrary file paths - real given finding
  1 above, but restricting the pattern risks breaking legitimate
  multi-key scenarios (enrolling a passenger's key); a genuine
  design tradeoff, not a one-line fix.
- Go's `captureOutput` swapping the process-global `os.Stdout`/`os.Stderr`
  (`helper/session/main.go`) - already documented as safe only because
  `dispatch()` is never called concurrently with itself; there's no
  `io.Writer`-injection point in upstream's `execute()`/`Handler` to
  avoid the swap without hand-editing the vendored file, so left as-is.
- `session_client.rs`'s `run()` holding its mutex for the full BLE op,
  which means a concurrent `invalidate()` (from `SetConfig`/
  `GenerateKey`) blocks for that whole duration, not just until idle -
  added a doc comment on `invalidate()` explaining this; inert since the
  feature is off by default and unverified on hardware.

**Still not verified on-device** - none of this was tested against a
real vehicle (same standing limitation as every other entry in this
file). Verified instead: `cargo build`/`test`/`clippy --all-targets -D
warnings` clean and cross-compiles for `aarch64-unknown-linux-musl`;
QML/JS syntax checked (`node --check`, brace balance) for every touched
file; `teslaclient.cpp` syntax-only compiles against real Qt6 D-Bus
headers.

## Added 2026-08-11 - live vehicle-status dashboard on FirstPage

Compared this app against `harbour-tcarint`, a different Sailfish Tesla
client (`../harbour-tcarint` locally). Its command coverage and D-Bus/helper
architecture aren't actually ahead of this app's (this app's catalog is
broader - key management, diagnostics, schedules, tonneau/media/software
commands tcarint doesn't have - and tcarint sidesteps the CAP_NET_ADMIN
problem by disabling Sailjail sandboxing entirely, trading away Harbour
Store eligibility for the whole app rather than just the helper). Its real
edge was UX: a live Tesla-app-style dashboard (lock/climate/battery state
bound to real vehicle data) versus this app's flat command list with no
sense of current vehicle state.

Ported the useful part: `FirstPage.qml` now shows a status card (locked/
unlocked, battery %, inside temp) that's tappable to toggle lock and
climate, backed by three chained `teslaClient.runCommand("status:...",
"state", [category])` calls (`closures_state`, `climate_state`,
`charge_state` - `CategoryPage.qml`'s existing "Get Vehicle State" command
already covers all the BLE-readable categories). Chained rather than
parallel since each is its own BLE connect+handshake through a fresh
`tesla-control` subprocess; three concurrent ones would just contend for
the same adapter. `CategoryPage.qml` gets the same status object passed
through at push time and shows a one-line subtitle for the categories it's
relevant to, with no extra BLE round-trip.

No helper changes needed - `"state"` was already in
`helper/src/commands.rs`'s `COMMAND_CATALOG` and already wired through
`TeslaClient::runCommand`, just never called from `FirstPage.qml`. New
file: `app/qml/js/VehicleState.js`, parsing `tesla-control state`'s
`protojson.Format` output into a flat status object.

One real ambiguity, documented in that file: `protojson.Format` omits any
field still at its zero value, so a missing JSON key means "false"/"0",
not "unknown" - correct to treat the same way for every field surfaced
here except battery level 0%, which is indistinguishable from "not fetched
yet" through this CLI wrapper.

**Not verified on-device** - no phone/car available in this environment.
QML/JS syntax was checked (brace balance, `node --check` on the `.js`
file's body), and the D-Bus call shape matches `CategoryPage.qml`'s
existing, working "Get Vehicle State" command exactly, but the actual
`protojson` field names (`insideTempCelsius`, `batteryLevel`,
`doorOpenDriverFront`, etc., taken from `vehicle.proto` at the README's
pinned `v0.4.1` tag) haven't been confirmed against a real
`tesla-control state` reply. Verify field names and the toggle-then-
refetch flow against a real vehicle before relying on this.

### Fixed 2026-08-11 - dashboard status never loaded (wrong CATEGORY names + unparsed VehicleData wrapper)

First real-device test of the above: lock/unlock worked, the status card
never populated. Root-caused against the actual v0.4.1 source (not just
the vendored, older `harbour-tcarint` copy used above), two bugs stacked:

1. `CATEGORY` values were guessed as the Fleet API's `vehicle_data` JSON
   section names (`closures_state`, `climate_state`, `charge_state`, plus
   `vehicle_state`/`gui_settings`/`vehicle_config` which don't exist as
   `state` categories at all). `cmd/tesla-control/commands.go`'s
   `GetCategory`/`categoriesByName` actually uses short, hyphenated names
   with no `_state` suffix: `charge`, `climate`, `drive`, `location`,
   `closures`, `charge-schedule`, `precondition-schedule`, `tire-pressure`,
   `media`, `media-detail`, `software-update`, `parental-controls`.
   `GetCategory` rejects an unrecognized name *before touching BLE*, so
   this failed fast and deterministically - which is why it looked like
   "never loads" rather than "slow like everything else over BLE." This
   bug predated the dashboard: `CommandCatalog.js`'s existing "Get Vehicle
   State" diagnostics command had the same wrong enum values and was
   presumably never actually exercised before.
2. Even with the right name, `car.GetState()` (`pkg/vehicle/state.go`)
   returns a `VehicleData` wrapper message
   (`pkg/protocol/protobuf/vehicle.proto`), so the JSON is
   `{"closuresState": {...fields...}}`, not the fields directly at the top
   level - `VehicleState.js`'s merge functions weren't unwrapping that.

Fixed `CommandCatalog.js`'s CATEGORY enum, `FirstPage.qml`'s three
`runCommand(..., "state", [category])` calls, and `VehicleState.js`'s
`merge*` functions (now read `JSON.parse(jsonText).closuresState` etc.).

**Correction to this entry (2026-08-11, later the same session):** the
paragraph originally here claimed lock/unlock use VCSEC exclusively and
so work even while the car's infotainment is asleep, unlike `state`
categories. That was an over-generalization from `GetState`'s doc comment
and turned out to be wrong once checked against the actual pinned source
(`cmd/tesla-control/commands.go` at the `v0.4.1` tag): only
`body-controller-state` sets `domain: protocol.DomainVCSEC` explicitly
(confirmed by grepping the whole file - exactly one match). Every other
command, lock/unlock included, leaves `Command.domain` unset, so
`car.StartSession(ctx, nil)` establishes **all** domains (VCSEC +
Infotainment) by default - `StartSession`'s own doc comment: "If domains
is nil, then the client will establish connections with all supported
vehicle subsystems." So lock/unlock pay the same dual-domain handshake
cost as everything else; the earlier "unlike lock/unlock" claim should be
read as "unlike body-controller-state" instead. Practical effect: this
makes the persistent-session work below more valuable than originally
scoped (every authenticated command reconnects fully today, not just
`state` ones), and means "vehicle asleep" isn't actually a confirmed
explanation for the dashboard failing to refresh - it's still plausible
(Infotainment being asleep can still affect any dual-domain handshake),
just not verified, and not the VCSEC-vs-Infotainment story originally
written here.

**Still not verified on-device** - confirm the corrected category names
and the `closuresState`/`climateState`/`chargeState` unwrap against a
real `tesla-control state <category>` reply.

## Added 2026-08-11 - optional persistent BLE session (tesla-session), off by default

Scoped and implemented per the design discussion in this session: every
authenticated command today pays a full BLE connect + dual-domain
`StartSession` handshake, because `Run()` execs a fresh `tesla-control`
subprocess per command (see the correction above - this turned out to
apply to lock/unlock too, not just `state`/`climate`/`charge`). Added
`helper/session/`, a small persistent Go companion (`tesla-session`) that
keeps one `*vehicle.Vehicle` connection alive across many commands
instead of reconnecting every time, torn down after 90s of inactivity so
the vehicle and phone radio aren't held awake indefinitely by an idle
app.

Design choices, and why:
- **Reuses upstream's own command dispatch, not a third hand-copy of it.**
  `helper/session/commands_vendor.go` is a byte-for-byte copy of
  `cmd/tesla-control/commands.go` at the pinned `v0.4.1` tag (sha256 in
  the file's header comment) - `execute()` and the `commands` map are used
  as-is. Only `main.go` is new: upstream's own `main.go` connects once and
  exits after one command; this one holds the session across many and
  adds the idle-timeout/reconnect logic, which has no upstream
  equivalent to reuse.
- **`teslacontrold` falls back to today's one-shot exec on any
  session-layer failure** (`helper/src/session_client.rs`) - spawn
  failure, broken pipe, timeout, a response whose `id` doesn't match the
  request. A bug in the new component degrades to current behavior, not
  to a broken app. This fallback is permanent, not just for rollout.
- **Off by default** - `TESLACONTROLD_PERSISTENT_SESSION` unset means
  `Helper.session` is `None` and `Run()`'s code path is byte-identical to
  before this feature existed.
- **Not a Settings toggle** - ships as an invisible optimization (decided
  with the maintainer: simpler than exposing a third connection state to
  explain, and the fallback already bounds the downside).
- `SetConfig`/`GenerateKey` call `session.invalidate()`, since a live
  session has the old VIN/timeouts baked into its `argv` and the old
  private key loaded into memory - both would otherwise go stale
  silently instead of erroring.

Verified in this environment (no phone/car available, see below):
`helper/session` builds and cross-compiles cleanly
(`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build`, pure Go, no Docker
needed) and its tests pass - readiness-check behavior via the vendored
`execute()`/`commands` map directly (unknown command, missing VIN,
FleetAPI-only command with no account), stdout/stderr capture/isolation,
and a missing-private-key connect failure surfacing as a clean error
response rather than a panic or hang. `helper/` (Rust) builds, its full
test suite passes, `cargo clippy --all-targets -- -D warnings` is clean,
and it cross-compiles for the real target
(`aarch64-unknown-linux-musl`, via the same `messense/rust-musl-cross`
Docker image the existing build already uses).

**Not verified on-device, and the flag is intentionally off until it
is:**
- Idle-teardown actually happening after 90s (timer logic reviewed, not
  observed).
- Reconnect-after-idle-teardown working transparently.
- `ble_sem`'s single-BLE-op-at-a-time guarantee holding up with the
  session path in the mix (should be untouched - `ble_sem` still wraps
  the same call site - but "should be" isn't "observed").
- The actual latency win (`lock` then `climate-on` back-to-back,
  before/after) - the whole point of this feature and the one thing that
  can't be checked without real hardware.
- Whether `tesla-session`'s own stderr (inherited into `teslacontrold`'s,
  so it should reach `journalctl -u teslacontrold`) actually shows up
  there as intended.

To try it: `TESLACONTROLD_PERSISTENT_SESSION=1` in
`teslacontrold.service`'s environment (or `systemctl edit --runtime` for
a throwaway test), reinstall/restart, then watch behavior against a real
vehicle before flipping any default.

## Fixed 2026-08-11 - "Pairing failed: ... can't init hci: ... operation not permitted" despite correct setcap

First real pairing attempt on the phone (post Rust rewrite) failed with
`tesla-control`'s own BLE-adapter error, verbatim suggesting the exact fix
already in place: `sudo setcap 'cap_net_admin=eip' "$(which
/opt/teslacontrold/bin/tesla-control)"`. Root-caused over SSH, in order:

- `getcap /opt/teslacontrold/bin/tesla-control` confirmed `cap_net_admin=eip`
  really was set on-disk - not a redeploy-stripped-the-xattr problem.
- Ruled out `nosuid` (root fs is `ext4 rw,noatime,seclabel`, no `nosuid`)
  and SELinux (the binary, and even the device's own stock `bluetoothd`/
  `connmand`, are all `unlabeled:s0`; the one real AVC hit seen was
  unrelated and `permissive=1`) as the file-capability blocker.
- Running the *exact same binary as the exact same `teslacontrol` user*
  directly via `runuser -u teslacontrol --`, bypassing `teslacontrold`/
  systemd entirely, worked (`Error: context deadline exceeded` against a
  placeholder VIN - i.e. it got past HCI init, same result as the
  original Phase 0 feasibility test in README.md). So the capability
  grant itself was fine; the bug was specifically in how
  `teslacontrold.service` runs its child.
- Bisected the unit's sandboxing directives via `/proc/<pid>/status`'s
  `NoNewPrivs` field (checked directly, not inferred): despite the unit's
  explicit `NoNewPrivileges=false`, the live process showed `NoNewPrivs: 1`.
  Clearing `SystemCallFilter=`/`SystemCallErrorNumber=`/`RestrictNamespaces=`
  via a `systemctl edit --runtime` drop-in successfully removed the
  resulting seccomp filter (`Seccomp_filters: 0`) but did **not** flip
  `NoNewPrivs` back to `0`. Editing the *base* unit fragment directly (not
  a drop-in) to permanently drop those three, plus `AmbientCapabilities=`,
  still left `NoNewPrivs: 1`. Only dropping
  `ProtectSystem=`/`ProtectHome=`/`PrivateTmp=`/`ProtectKernelTunables=`/
  `ProtectControlGroups=` from the base fragment finally got `NoNewPrivs: 0`
  - confirmed via a from-scratch throwaway unit with zero hardening
    directives (clean `NoNewPrivs: 0`) as a control, ruling out "systemd on
    this device always forces NNP for `User=` services" as an alternative
    explanation.
  - On this device's systemd 238, declaring any of that `Protect*`/
    `PrivateTmp` family (even together with an explicit
    `NoNewPrivileges=false`, and even if later neutralized by a drop-in)
    makes systemd force kernel `PR_SET_NO_NEW_PRIVS=1` on the whole unit's
    process tree anyway. Since NNP is one-way and inherited by children,
    that silently strips the exec'd `tesla-control` child's ability to
    pick up `CAP_NET_ADMIN` from its own file capability - producing
    exactly this misleading "operation not permitted" error, which reads
    like a missing `setcap` even though `getcap` shows it's correctly
    applied.
  - `AmbientCapabilities=CAP_SYS_PTRACE` (needed for `authorize()`'s
    cross-UID `/proc/<pid>/exe` read, see the entry below) was separately,
    individually verified *not* to trigger this on its own.

**Fix applied:** `helper/systemd/teslacontrold.service` no longer declares
`ProtectSystem=`, `ProtectHome=`, `PrivateTmp=`, `ProtectKernelTunables=`,
`ProtectControlGroups=`, `RestrictNamespaces=`, `SystemCallFilter=`, or
`SystemCallErrorNumber=` (and the now-meaningless `ReadWritePaths=`, which
only did anything alongside `ProtectSystem=strict`). `AmbientCapabilities=
CAP_SYS_PTRACE` and the explicit `NoNewPrivileges=false` are kept.
Verified end-to-end on-device after this change: pairing succeeded, and a
`Lights → Flash` command actually flashed the car's lights. RPM version
bumped to 0.1.2. Not yet repackaged as an RPM - the checked-in
`helper/RPMS/teslacontrold-0.1.1-1.aarch64.rpm` now predates this fix and
needs rebuilding before shipping (the on-device fix was applied by hand-
editing the installed unit file directly, not via reinstalling a rebuilt
package).

## Rewritten 2026-08-10 - teslacontrold ported from Go to Rust

`teslacontrold` (`helper/`) was rewritten from Go to Rust (zbus, blocking
API), at the maintainer's request, for maintainability - not in response
to a bug. `tesla-control`/`tesla-keygen` are unaffected: they're still
cross-compiled straight from upstream `teslamotors/vehicle-command` (Go)
and exec'd as subprocesses, never linked in.

Everything outside `helper/` needed zero changes: the D-Bus bus name,
object path, interface name, and every method's wire signature are
byte-for-byte identical, so `app/src/teslaclient.cpp` didn't change. The
RPM spec, systemd unit, D-Bus policy/service files, and Sailjail
permission file also needed no changes.

Behavior deliberately preserved during the port (see `helper/src/`'s
module doc comments for the "why" behind each):
- The `i64`-before-summing timeout arithmetic fix (avoids an `i32`
  overflow producing a negative/already-cancelled deadline).
- The exact D-Bus error names (`org.teslacontrol.Helper1.Forbidden` etc.)
  via a `#[derive(zbus::DBusError)]` enum.
- All `authorize()` log points, including the fix earlier in this file
  (the `/proc/<pid>/exe` readlink failure branch that used to fail
  silently) - it logs now in the Rust version too.
- The PID-reuse TOCTOU UID cross-check.
- Atomic config writes (`.tmp` + rename).
- Two asymmetries in the original Go code that look like bugs but were
  preserved as-is rather than silently "fixed" during a rewrite that
  wasn't supposed to change behavior: `Run()`'s BLE-busy condition is a
  hard D-Bus error (`.Busy`), while `Pair()`'s identical condition
  returns a normal `ok=false` reply instead; and `GenerateKey()` holds
  the config mutex for its whole subprocess call even though it never
  reads the config (kept anyway, since it still serializes concurrent
  key generation against writing the same key files).

One correctness detail that doesn't map 1:1 from Go and needed actual
thought rather than direct translation: zbus's own docs warn against
using its blocking API for an outbound proxy call (`GetConnectionCredentials`,
needed by `authorize()`) from *within* an interface method that's being
dispatched by that *same* connection - a reentrancy hazard zbus calls the
"async sandwich" footgun, since the blocking call there would need to
`block_on` on the very connection whose event loop is what's currently
calling it. `Helper` sidesteps this by opening a second, independent
`zbus::blocking::Connection` used only for that one outbound call -
distinct from the connection the `ObjectServer` uses to serve the object.
This has no equivalent concern in the original Go version, since
goroutines make this kind of concurrent I/O implicit rather than
something the type system forces you to reason about.

Verified before considering this done: `cargo build`/`clippy`/`test`
clean; cross-compiled to `aarch64-unknown-linux-musl` (3.5MB static
binary, actually smaller than the 4.1MB Go one); smoke-tested against a
real, separate D-Bus daemon acting as a stand-in system bus (not just
unit tests) - `GetConfig`, `SetConfig` (both valid and validation-rejected
input), `Run` (unknown command, `-`-prefixed arg, and a real subprocess
exec via fake `tesla-control`/`tesla-keygen` stand-ins), `GenerateKey`,
`Pair`, the `Forbidden` rejection path with the exact expected error
message and log line, and the timeout-and-kill path (confirmed no leaked
child process and the daemon stayed healthy and responsive for later
calls afterward). Also confirmed on the actual phone: deployed via the
same hot-patch route established in the entries below, checksum-verified
against the local build, and re-ran the same `dbus-send`/`journalctl`
checks - identical `Forbidden`/rejection behavior to the Go version, plus
idle RSS dropped from ~2MB to ~244KB. The app itself (unchanged,
`teslaclient.cpp` doesn't know or care what the daemon is written in)
was closed and reopened against the new daemon and looked identical.
Not yet packaged as an RPM - see "Build verification status" in
README.md.

## Fixed 2026-08-10 - on-device: "helper service not found" despite teslacontrold running

First real end-to-end test on a Jolla Phone 2026 (Sailfish 5.2.0.16):
`teslacontrold` was active with no errors in `systemctl status`, but the
app still showed the "helper service not found" banner, and no error
logs were visible. Root-caused over SSH:

- Directly reproduced with `dbus-send --system ... GetConfig`:
  `Error org.teslacontrol.Helper1.Forbidden: cannot resolve caller binary`.
  `authorize()` in `helper/main.go` was rejecting *every* caller, not just
  unauthorized ones.
- Cause: `authorize()` resolves the caller's binary via
  `os.Readlink("/proc/<pid>/exe")`. `teslacontrold` runs unprivileged as
  its own `teslacontrol` user with no `CAP_SYS_PTRACE`. Reading another
  UID's `/proc/<pid>/exe` symlink requires ptrace access, which the
  kernel denies cross-UID without that capability - confirmed
  `/proc/sys/kernel/yama/ptrace_scope` was `1` on-device, and the
  `teslacontrold.service` unit granted no capabilities at all. So the
  read failed for 100% of callers, always. This is also the one
  rejection branch in `authorize()` that had no `log.Printf` call -
  not a logging oversight, just the one silent path, and the one
  actually being hit, which is why nothing showed up in the journal.
- While fixing this, verified the *other* open question below (Sailjail
  proxy PID) is also real, not just theoretical: `firejail --debug`
  showed the `xdg-dbus-proxy` filter generated from
  `TeslaControlHelper.permission` correctly includes
  `--talk=org.teslacontrol.Helper` (sandbox routing itself is fine), but
  `busctl list --system` showed the app's live system-bus connections
  owned by PID = `/usr/bin/xdg-dbus-proxy`, never `harbour-teslacontrol`'s
  own PID. So even with `CAP_SYS_PTRACE` granted, matching against
  `/usr/bin/harbour-teslacontrol` alone would never succeed for a real
  sandboxed call.
- Also checked the SMACK-label fallback this file previously suggested as
  the alternative: not viable on this device - `/sys/fs/smackfs` doesn't
  exist, and every process (app, proxy, firejail) reported the same
  generic SELinux-style context (`u:r:kernel:s0`), so there's no per-app
  label to check via `GetConnectionCredentials`'s `LinuxSecurityLabel`.

**Fix applied:**
- `helper/systemd/teslacontrold.service`: added
  `AmbientCapabilities=CAP_SYS_PTRACE` so `authorize()` can actually read
  a cross-UID caller's `/proc/<pid>/exe`. `CapabilityBoundingSet` is
  deliberately left unset (full default) - restricting it would also
  narrow what capabilities the setcap'd `tesla-control` child can pick up
  on exec (the kernel intersects a child's file capabilities with the
  parent's bounding set), breaking BLE.
- `helper/main.go`: `TESLACONTROLD_ALLOWED_CALLERS` default changed from
  `/usr/bin/harbour-teslacontrol` to
  `/usr/bin/harbour-teslacontrol,/usr/bin/xdg-dbus-proxy`, since the
  proxy's own exe - not the app's - is what a real sandboxed call
  resolves to. The practical per-app boundary is Sailjail's own
  permission-scoped proxy filter (only an app declaring the
  `TeslaControlHelper` permission gets this bus name into its proxy's
  filter at all); this allow-list's remaining job is blocking a rogue
  *unsandboxed* process connecting directly as `defaultuser`, which it
  still does since such a process's own exe matches neither entry.
- `helper/main.go`: the previously-silent `/proc/<pid>/exe` readlink
  failure branch in `authorize()` now logs, so a regression here (e.g.
  the capability grant getting dropped) shows up in
  `journalctl -u teslacontrold` instead of manifesting only as a client-
  side "not found" banner with nothing on the daemon side.

Not yet re-verified end-to-end after this fix (rebuild + reinstall +
retest against the running app was not repeated in this pass) - do that
before relying on it.

## Fixed 2026-08-10 (continued) - banner still stuck after the daemon-side fix

After redeploying the `teslacontrold` fix above, `teslacontrold`'s own
journal proved the D-Bus call was reaching it and being authorized
(`authorized call from pid ... (/usr/bin/xdg-dbus-proxy)`), yet the app's
"helper service not found" banner still didn't clear, and pulling down
"Refresh" produced no new daemon-side log line at all. Root-caused via
the *app's* own journal output (`journalctl -t harbour-teslacontrol`):

```
TypeError: Cannot call method 'refreshHelperAvailable' of undefined
```

at `FirstPage.qml:13`, inside `refresh()`. Cause: a QML scoping
gotcha in `app/qml/harbour-teslacontrol.qml`:

```qml
TeslaClient {
    id: teslaClient
}
initialPage: Component {
    FirstPage {
        teslaClient: teslaClient
    }
}
```

`FirstPage.qml` declares its own `property var teslaClient`. Inside an
*inline declarative object literal* like `FirstPage { teslaClient:
teslaClient }`, QML resolves the right-hand `teslaClient` against the
newly-constructed FirstPage instance's own scope first - and since that
instance already has a property of that exact name, the outer `id:
teslaClient` gets shadowed. The binding becomes a no-op self-assignment
(`this.teslaClient = this.teslaClient`, both `undefined`), so
`FirstPage.teslaClient` was never actually set. This also explains the
one log line that *did* appear: it came from `TeslaClient`'s own C++
constructor calling `refreshHelperAvailable()` on itself directly,
which doesn't go through this broken QML property at all - so it always
"worked" once, in isolation, while `FirstPage`'s own `refresh()` (from
`Component.onCompleted` or the pull-down "Refresh" menu item) silently
threw and never made a D-Bus call. The banner's own
`visible: !teslaClient.helperAvailable` binding throws for the same
reason and falls back to the Rectangle's default `visible: true`, so it
was never actually reactive to the helper's real state.

Checked every other `{ teslaClient: teslaClient }` occurrence in
`app/qml/`: all the others are JS object literals passed as the second
argument to `pageStack.push(url, { teslaClient: teslaClient })`, called
from *within* a page's own method/handler - there `teslaClient`
unambiguously resolves to that page's own property (`page.teslaClient`),
which is a different, safe evaluation context from a declarative inline
child-object binding. Only the one occurrence in
`harbour-teslacontrol.qml` was affected.

Also surfaced along the way: `harbour-teslacontrol.desktop`'s launcher
uses `--single-instance` (visible via `ps`: `invoker --type=silica-qt5
--id=harbour-teslacontrol --single-instance harbour-teslacontrol`), so
backgrounding the app via the multitasking view and relaunching from the
app grid resumes the existing process rather than starting a fresh one -
`Component.onCompleted` won't re-fire and QML edits won't take effect
until the process is actually killed (`pkill -f harbour-teslacontrol`)
first. Worth remembering for any future on-device QML debugging.

**Fix applied:** renamed the outer id in `harbour-teslacontrol.qml` from
`teslaClient` to `teslaClientInstance` so it no longer collides with
`FirstPage`'s property name of the same name.

Not yet re-verified on-device after this fix either - confirm the banner
actually clears after redeploying before considering this closed.

## Fixed in this pass

- **D-Bus caller authorization** (`helper/main.go`): `Run`, `GenerateKey`,
  `Pair`, `SetConfig`, and `GetConfig` now resolve the calling process's
  PID **and UID** atomically via `org.freedesktop.DBus.GetConnectionCredentials`
  and check `/proc/<pid>/exe` against an allow-list
  (`TESLACONTROLD_ALLOWED_CALLERS`, default
  `/usr/bin/harbour-teslacontrol,/usr/bin/xdg-dbus-proxy` - see the
  2026-08-10 entry above for why the proxy binary is in there too),
  denying by default on any lookup failure. The PID's current UID read from
  `/proc/<pid>/status` is cross-checked against the credentials reply, which
  closes the PID-reuse/TOCTOU race (a recycled PID whose UID no longer matches
  the connection's recorded UID is rejected). Previously the D-Bus system
  policy (`org.teslacontrol.Helper.conf`) only scoped access to the
  `defaultuser` Unix account - Sailfish being single-user, that meant *any*
  process running as `defaultuser` could call these methods directly (unlock,
  honk, open trunk, valet mode...), not just `harbour-teslacontrol`, since the
  Sailjail `.permission` file only filters D-Bus for apps launched *through*
  the sandbox, not arbitrary unsandboxed processes. **This closes the gap for
  unsandboxed rogue processes/scripts running as `defaultuser`**; per-sandboxed-
  app distinction beyond that isn't achievable on this device (see 2026-08-10
  entry above) and is instead delegated to Sailjail's own permission-scoped
  proxy filter.
- **Timeout hardening** (`helper/main.go`): `SetConfig` now rejects non-positive
  timeouts *and* timeouts above 300s, and the per-command deadline is computed
  in `int64` so an overflowing `int32` sum can no longer turn into a negative
  (already-cancelled) context deadline.
- **Concurrency limit on BLE** (`helper/main.go`): `Run` and `Pair` now
  serialize through a capacity-1 semaphore; a second simultaneous BLE command
  is rejected with a `org.teslacontrol.Helper1.Busy` error instead of spawning
  a second `tesla-control` that fights for the HCI adapter.
- **Audit logging + secret redaction** (`helper/main.go`): every `Run`/`Pair`/
  `GenerateKey`/`SetConfig` is now logged. Subcommands that take a PIN
  (`valet-mode-on`, `parental-controls-on/off`, `parental-controls-clear-pin-admin`)
  log a redacted `[N redacted args]` instead of writing the PIN to the system
  journal in cleartext.
- **Atomic config writes** (`helper/main.go`): `config.json` is now written to
  a `.tmp` sibling and `os.Rename`d into place, so a crash mid-write can no
  longer truncate/zero the file and silently reset the VIN/key/timeouts to
  defaults; an unparseable config is now logged rather than silently ignored.
- **VIN validation** (`helper/main.go`): `SetConfig` rejects VINs that aren't
  17 `[A-HJ-NPR-Z0-9]` chars (allowed empty, to clear).
- **Argument dialog validation** (`app/qml/pages/ArgumentDialog.qml`): the Run
  button is now disabled until all *required* fields are filled, via an
  explicit `formValid` property recomputed by `revalidate()` on every field
  change (a plain `canAccept` binding can't track the catalog's JS-object
  `__value` fields and would leave the button permanently disabled).
- **systemd sandboxing** (`helper/systemd/teslacontrold.service`): the service
  now runs with `ProtectSystem=strict` + `ReadWritePaths=/var/lib/teslacontrold`,
  `ProtectHome=true`, `PrivateTmp=true`, `ProtectKernelTunables=true`,
  `ProtectControlGroups=true`, `RestrictNamespaces=true`, and
  `SystemCallFilter=@system-service`. These are **not yet verified on a real
  device** - if `teslacontrol` fails to bring up the HCI adapter after install,
  these directives are the first thing to relax (see the unresolved section).
- Repo hygiene: prebuilt binaries, built RPMs, rpmbuild staging output,
  and build logs are no longer tracked in git (`.gitignore` added); they
  remain on disk locally where useful (`spike/bin/`, `helper/dist/`,
  `*/RPMS/`) so existing local builds aren't lost.

## Unresolved - needs on-device verification

- **The systemd sandbox directives added in the hardening pass
  (`ProtectSystem=strict`, `SystemCallFilter=@system-service`, etc.) are
  unverified on-device.** go-ble/ble drives the HCI adapter through raw
  sockets and ioctls, which are covered by `@system-service`, but if the
  service logs a BLE init failure after install, comment out the
  `ProtectSystem`/`SystemCallFilter`/`RestrictNamespaces` lines, retest, and
  re-add the least restrictive set that still boots cleanly before shipping.
- **Cross-page D-Bus reply collisions in the UI** (`app/qml/pages/`):
  `CategoryPage.qml`'s "keys" category and `PairingPage.qml` both issue
  `runCommand("list-keys", "list-keys", [])` - i.e. the same `requestId`.
  Sailfish's `PageStack` keeps prior pages alive (not destroyed) after
  navigating forward, so if both pages are simultaneously present in the
  stack, both pages' `Connections` blocks match the same reply and both
  update their own result label, even though only one triggered the
  call. Harmless (no crash, no wrong data - both show the same real
  result), but not asymptotically correct. Fixing properly means giving
  every call a unique `requestId` (e.g. a counter or uuid) instead of the
  command name, which needs the app running to confirm `PageStack`
  lifecycle assumptions.
- **`CommandCatalog.js` argument definitions are shared, mutable
  singletons.** `.pragma library` means `CATEGORIES` is created once and
  reused for the life of the app; `ArgumentDialog.qml` writes the user's
  input onto the same `arg()` object (`argSpec.__value = ...`) rather
  than a copy. In practice this doesn't currently cause visibly wrong
  behavior, because every field type re-derives its value from the
  catalog's `def`/`min`/`max` on each dialog open (`Component.onCompleted`
  always overwrites `__value`), but it's fragile: any future field type
  that doesn't re-sync on load would silently leak state between
  unrelated dialog opens. Worth cloning `commandDef.args` before passing
  it to `ArgumentDialog.qml` in `CategoryPage.qml` if this file changes.

## Pre-existing, from the original spike (still true)

- The Silica app (`app/`) has not been compiled against real Qt5/Silica
  headers - only syntax-reviewed and cross-checked against the D-Bus
  method signatures in `helper/main.go`. Run an `sfdk build` pass before
  trusting it.
- `CommandCatalog.js` argument bounds/enum values (`STATE` on/off,
  `ROLE`, `FORM_FACTOR`, `state` `CATEGORY` names, etc.) are best-effort
  from public docs, not verified against `tesla-control help <cmd>` on
  the exact upstream binary version in use.
- `app/icons/` are placeholders.
- `spike/bin/tesla-control` and `spike/bin/tesla-keygen` are prebuilt
  binaries built once via Docker from `teslamotors/vehicle-command`'s
  default branch (`git clone --depth 1`, no commit pin). They're kept
  locally (gitignored, not committed) for convenience, but since they
  get `setcap cap_net_admin=eip` and run with real capability on the
  phone, rebuild them fresh from a specific pinned commit/tag before
  packaging for actual use, rather than trusting the checked-out copy.
- Fleet API / internet mode is intentionally out of scope for v1 - BLE
  only.
