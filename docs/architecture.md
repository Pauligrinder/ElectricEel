# Architecture

Single Harbour RPM (`harbour-electric-eel`). No system service, no
capabilities, no `devel-su` install. Sailjail permissions: `Bluetooth;Documents`
(`Documents` covers the `Documents/ElectricEel` phone-key logs).

## Components

- **Application entrypoint** (`helper/src/app.rs`, Rust, `app-entry` feature):
  exports `electric_eel_app_main` from the static library, called by a thin
  C++ `main` forwarding function. It calls Qt's
  platform setup to resolve Sailjail paths, constructs and owns the runtime,
  runs the UI with a borrowed runtime handle, then joins Rust's threads,
   stops presence and joins the Go library before Qt teardown. There is one application
  process; the UI does not construct, start, poll or free the Rust core.
- **Application runtime** (`helper/src/runtime.rs`): starts phone-key mode
  independently of the UI. An async control loop serves configuration reads,
  snapshots and destination previews while a separate serial executor handles
  commands, pairing, key generation, configuration mutations and presence
  lifecycle work. Both UI queues are bounded at 64. Presence events wake the
  control loop immediately, with a one-second fallback drain; restarting
  presence never runs in the event drain. The executor drives five-second
  retries and coalesced resume recovery. Session-generation tags reject stale
  events and start results. The control loop owns the published phone-key
  state and the 2.5-second refresh deadline, measured from command completion.
  A separate Rust lease thread keeps renewing during BLE work.
- **Silica UI** (`app/qml`, `app/src`): the `TeslaClient` QObject is injected
  into QML by the UI callback. It serializes UI actions to `runtime_submit`
  and copies pushed Rust notifications into queued Qt deliveries, updating
  properties and emitting signals on the GUI thread. Callback detachment is
  a synchronization barrier before UI destruction. There are no Qt worker
  threads or backend timers. QML retains only the dashboard's label-age
  timer, which updates presentation without touching the car. QML pages:
  FirstPage (dashboard + categories), CategoryPage (generic command list),
  ArgumentDialog (per-command form), PairingPage, SettingsPage,
  NavigationPage. Configuration-load failures are explicit errors rather than
  fabricated empty data. Dashboard requests are tagged by page, sequence and
  VIN; changing VIN clears telemetry and rejects late replies. Per-category
  ages keep partial refreshes from making retained readings appear fresh.
- **Control core** (`helper/src`, Rust staticlib `libelectriceelcore.a`,
  C ABI via cbindgen `electriceelcore.h`): config + key files
  (`config.json`, `private_key.pem`, `public_key.pem` under the app data
  dir), command allow-list, destination parser (`share.rs`) and typed command
  parsing (`protocol_commands.rs`). Keys/PEM persistence live in `keys.rs`.
  One foreground BLE command at a time (`ble_sem`).
- **Phone-key controller** (`helper/src/protocol_client.rs`, `phone_key.rs`):
  Rust owns proximity, target eligibility, reconnect backoff, idle retention,
  periodic status, inside inference and NFC enrollment polling. Its controller
  and logging/idle maintenance run independently of the UI executor.
- **Protocol library** (`helper/session/library`, Go `libteslable.so` in
  `/usr/share/harbour-electric-eel/lib/`): holds authenticated BLE sessions and
  executes typed operations through an async C ABI. It calls pinned
  vehicle-command v0.4.1 directly, without the CLI dispatcher. Passive-entry
  responses are independent of foreground commands. Navigation uses
  the existing vehicle protocol methods
  (field-53/field-21 actions, see `navigation-share.md`).
  Fresh and reused links authenticate only the domain required by the command:
  passive entry, locks and body-controller actions use VCSEC, while climate,
  charging, media, windows and navigation use infotainment.

## Asynchronous protocol ABI

`helper/session/abi/tesla_ble.h` defines ABI v2. Rust checks the private library's
version and symbols before creating sessions. The Go runtime remains loaded for
process lifetime; session replacement never unloads it.

Rust submits typed operations with monotonic request IDs and awaits copied
completion data. Serialized callbacks can race submission return and run on Go
threads; they only copy/enqueue/wake Rust state. The app uses no communication
socket, JSON frame envelope, subprocess, hello handshake or heartbeat.

Go reports fresh advertisements with opaque time-bounded target tokens, link
loss, authentication outcomes, watcher failures and diagnostics. Rust derives
near/far/inside events. Advertisements and derived state snapshots can coalesce;
command completions and terminal lifecycle notifications are reliable. Complex
telemetry retains protobuf-derived JSON result payloads.

Cancellation and stop bypass operation admission. Deadlines reach BLE/D-Bus
contexts. Uncertain or cancelled action streams are retired before reuse;
cancellation cannot undo a sent action. Only an explicit ATT write-length
rejection permits MTU fallback on a stream.

Shutdown stops admission, detaches Qt observers, cancels Go operations and joins
the Rust controller/maintenance threads. Go closes discovery, link observers,
vehicle and BlueZ resources. Destroy waits for the terminal callback itself to
return before Rust frees callback memory. A stuck teardown or teardown panic
cannot fabricate a successful ownership barrier.

Recoverable Go operation/background panics are logged with stacks and fault the
session; they are bugs to repair. Fatal runtime or dependency-goroutine failures
can still terminate the application. See [tesla-ble-abi.md](tesla-ble-abi.md).

Configuration changes are persisted before runtime settings/session changes.
Unknown newer schemas are read-only (or rejected if their shape is unsupported).
Explicit unpaired state survives restarts; only pre-phone-key V0 configurations
use the legacy key-file migration. Key generation validates reuse and requires
force to replace an unreadable key. Private/public key replacements are atomic
and synced, with enrollment reset persisted before key rotation.

Pairing requires the protocol library. After transmitting the NFC request,
Rust polls enrollment of the requested key for up to 90 seconds; pairing
this phone's key also requires a successful authenticated VCSEC handshake.
Transmission alone is never persisted as successful pairing.
Rust also checks that the public key belongs to this
phone's private key before touching BLE, so an interrupted key-file update
cannot pair a stale public half and mark a different private key enrolled.

## Bluetooth transport

Cooperative `org.bluez` D-Bus backend (`helper/session/bluez/`):
discovery, GATT connect, notify/write framing over the system bus. The
radio stays owned by the OS stack, so other Bluetooth users (e.g. a
soundbar) remain owned by the OS stack. Legacy socket/raw-HCI code is retained
only for standalone-daemon compatibility and regression fixtures; it is excluded
from the Harbour application build.

## Phone-key presence

After NFC pairing, the Rust controller runs a proximity loop
while the app is alive: it keeps an authenticated session up while the
vehicle beacon is near (RSSI hysteresis) and answers VCSEC
`AuthenticationRequest` messages, so handle-pull unlock and drive work
without tapping. It never locks/unlocks proactively; walk-away locking
stays the vehicle's own setting.

Disconnected discovery is continuous and event-driven: an independent BlueZ
signal subscription receives new devices and advertisement property updates.
Cached RSSI snapshots seed device identity only; they cannot trigger presence
connections. GATT links each own a fresh signal queue to avoid replaying an
old disconnect on a replacement connection.

While the core's `phone_key_enabled` flag is true, Rust's `CpuKeepAlive` holds an MCE
CPU lease on the system bus (`com.nokia.mce`, `/com/nokia/mce/request`). The
lease thread reads mode intent directly from the core's atomic flag, checking
every 100 ms independently of commands or UI callbacks. It acquires the lease
even while an initial presence start is still blocking. Failed starts and `presence_stopped` retries keep
the lease: connection status is separate from mode intent. A dedicated thread
queries `req_cpu_keepalive_period` and renews `req_cpu_keepalive_start` with the
stable ID `harbour-electric-eel-phone-key`, sleeping for half the granted period
(clamped to 100 ms–15 s). Blocking D-Bus calls have a one-second timeout; failures
retry after one second. Renewal needs neither a GUI timer nor a Qt event loop.
`req_cpu_keepalive_stop` releases the lease when mode is off or on shutdown.
Sailjail's base permission already permits MCE access.

Display blanking remains enabled. This trades increased screen-off power use
for prompt passive entry. Daily phone-key logs record the granted period, first
successful hold, every D-Bus failure, and display status (`0` unknown, `1` off,
`2` dimmed, `3` on), separately from Qt app lifecycle. Periodic wakeups cannot
preserve an authenticated GATT session and are not used.

Rust owns daily append handles, pruning, retention and the 10 MiB daily cap.
Go/BlueZ callbacks enqueue diagnostics for Rust's logging worker. Overflow is
counted and reported; private key material is not diagnostic data.

Phone-key events are also published on the session bus through
`org.electriceel.PhoneKey1`, including the fork's settled `presence_inside`
notification. See [phone-key-events.md](phone-key-events.md) for the signal
contract and integration examples.

The runtime serializes a single three-minute departure deadline per VIN/session.
It leaves `presence_far` immediate and emits `presence_away_confirmed` if no
`presence_inside` cancels the wait. Stops, restarts, identity changes and app
exit discard the deadline. Automagic hotspot flows only perform their respective
actions; no shared-state tokens or concurrent flow timers are required.

Settings exports an embedded Automagic import preset to a fresh Documents
folder through a bounded Rust background task. Qt supplies the platform's
Documents path and displays completion; filesystem work runs independently of
both control and BLE processing. Automagic handles importing and daemon reload.
The app needs neither cross-app configuration access nor a custom permission.
Runtime completion channels carry the serializable `Notification` enum, with
typed link states and internal session-generation metadata. JSON serialization
happens only when delivering a notification through the Qt C ABI; internal
dispatch does not use string tags or `serde_json::Value` lookups.

## Source layout

```
helper/src/{app,runtime,cpukeepalive,lib,core,ffi,config,commands,share,session_client}.rs
helper/src/{tesla_ble,protocol_client,protocol_commands,phone_key,keys,keylog}.rs
helper/src/automagic.rs           Documents export of the embedded import preset
helper/src/notification.rs        typed runtime-to-Qt notification contract
docs/automagic-hotspot/           canonical hotspot sources/actions/flows
helper/session/abi/              public C ABI and C smoke test
helper/session/cmd/teslable/     Go c-shared entrypoint
helper/session/library/          protocol operations, observations, auth responder
helper/session/bluez/            org.bluez D-Bus transport
helper/session/thirdparty/vehicle-command/   patched upstream (see vehicle-command-patch.md)
app/src/teslaclient.{h,cpp}      UI-only action/notification adapter
app/qml/{harbour-electric-eel.qml,cover/CoverPage.qml,pages/*.qml,js/*.js}
app/translations/                qsTr catalogs (see translations.md)
app/rpm/harbour-electric-eel.spec
helper/make-app-bundle.sh        stages Rust staticlib + private Go library
helper/build-go-library-sdk.sh   cgo cross-build against Sailfish's libc
```

`app/thirdparty/` and `app/lib/` are build output (gitignored),
regenerated by `make-app-bundle.sh`. The Rust staticlib targets
`aarch64-unknown-linux-gnu` (glibc, links against Qt); the Go library uses cgo
and the Sailfish target toolchain. The installed library has 0644 permissions;
both installed ELF files are stripped for Harbour.
