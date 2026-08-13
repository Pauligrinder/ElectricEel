# BlueZ D-Bus BLE backend — architecture notes

## Status: implementation complete, pending real-car validation

The BLE transport runs through a cooperative `org.bluez` D-Bus backend
instead of `go-ble`'s raw-HCI adapter takeover, and the app ships as a
single Harbour RPM with the Rust control core linked in-process (no
separate privileged daemon package). Both changes are done, tested (unit
tests + on-device SSH smoke tests with no car in range), and built for
real via the Sailfish Platform SDK.

**What's left is exclusively real-car testing** (scan→connect→session
round-trips, pairing, the soundbar-survival check) plus the one-time
on-device RPM install — nothing else in this design is open. This file no
longer needs to track day-to-day progress; see `README.md` for the current
architecture, build, and testing instructions, and `KNOWN_ISSUES.md` for
anything still genuinely unresolved.

## Why `org.bluez` instead of raw HCI

`go-ble`'s `socket.open()` does `HCIDEVDOWN`/`UP` and binds
`HCI_CHANNEL_USER` (exclusive), taking the adapter away from `bluetoothd`
for the whole process — any other Bluetooth user (e.g. a soundbar) gets
dropped for as long as the app holds a BLE session. Sailjail also applies
`caps.drop all` to every sandboxed app with no opt-out, so raw HCI would
need running unsandboxed regardless. `org.bluez` over D-Bus is what
`Bluetooth.permission` (the stock Sailjail grant) actually allows, and it
shares the adapter cooperatively with the rest of the OS.

The transport only touches two upstream call sites
(`ble.InitAdapterWithID` + `ble.NewConnection` in `helper/session/main.go`).
Everything downstream (`vehicle.NewVehicle`, vendored `execute()`, pairing)
consumes a plain `connector.Connector` interface, so `helper/session/bluez/`
implements that interface over `org.bluez` — a new package, no upstream
fork. See that package's own doc comments for the D-Bus call sequence
(discovery, GATT walk, notify/write framing); it's self-documenting there
rather than duplicated here.

## Why the Rust core is in-process, not a separate daemon

The original two-package split (sandboxed UI + privileged `teslacontrold`
system service reached over D-Bus) existed because raw-HCI BLE needed
capabilities the sandboxed app couldn't hold. Once BLE moved to
`org.bluez` — which the app's own Sailjail sandbox can already reach — that
reason went away, so the split was removable: the Rust core
(`helper/`) now builds as a `staticlib` (`crate-type = ["staticlib",
"rlib"]`) with a small `#[no_mangle] extern "C"` surface (opaque `Core`
handle, cbindgen-generated header `electriceelcore.h`), linked directly
into the app and driven from `app/src/teslaclient.cpp`'s worker thread.
The Go `tesla-session` BLE child is unchanged — bundled in the RPM and
spawned by the in-process core over the same stdin/stdout JSON protocol it
already used.

This is why `helper/src/{lib,core,ffi}.rs` and `app/src/teslaclient.{h,cpp}`
comments point back to this file: it's the record of *why* the crate is
shaped as a staticlib + C ABI instead of a D-Bus service, not a spec that
needs re-deriving from the code.

The legacy `teslacontrold`-style D-Bus daemon binary still exists behind a
`dbus` cargo feature (`electric-eel-daemon`, only built with
`--features dbus`) purely as a dev-diagnostics fallback; the app itself
never builds or links it.

## One-time verification commands

- `cd helper/session && go test -race ./...`
- `cd helper && cargo test && cargo clippy --all-targets -- -D warnings`
  (also `--features dbus` for the legacy daemon path)
- `./helper/make-app-bundle.sh` then the Docker `mb2` build in
  `README.md` §Build to produce a real RPM
