# ElectricEel — Sailfish OS GUI for tesla-control

A Silica (Sailfish OS) app that gives a Tesla-app-like GUI over
[teslamotors/vehicle-command](https://github.com/teslamotors/vehicle-command)'s
`tesla-control` CLI, controlling a Tesla vehicle over local BLE. Built and
tested against a Jolla Phone 2026 (aarch64, Sailfish OS 5.2.0.16).

## Why two packages

`tesla-control`'s BLE library (`go-ble/ble`) needs `CAP_NET_ADMIN` on a raw
HCI socket to bring the Bluetooth adapter up. Sailfish's Sailjail sandbox
(`/etc/sailjail/permissions/Base.permission`) applies `caps.drop all` to
*every* sandboxed app unconditionally — confirmed on-device, see
"Feasibility findings" below — so a Harbour-distributable, sandboxed app can
never do this itself, with or without `setcap`. Fix: split into two
packages.

- **`app/` → `harbour-teslacontrol`** — the sandboxed Silica UI. Harbour/Store
  compatible. Talks to the helper over the D-Bus *system* bus.
- **`helper/` → `teslacontrold`** — a small unsandboxed companion service
  that owns a `setcap`'d copy of the real `tesla-control`/`tesla-keygen`
  binaries and exposes them as D-Bus methods (`org.teslacontrol.Helper`).
  **Not Harbour-distributable** (ships a systemd service + capability grant);
  distribute via OpenRepos or install directly with `devel-su`.

This mirrors how Sailfish's own `Bluetooth.permission` grants sandboxed apps
D-Bus access to `org.bluez` rather than raw device access — same pattern,
just with a custom-installed permission (`TeslaControlHelper.permission`)
instead of a stock one, since Harbour review doesn't host third-party
system services.

## Feasibility findings (Phase 0 spike)

Tested directly against a Jolla Phone 2026 over SSH before building the GUI:

1. `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build` cross-compiles
   `tesla-control`/`tesla-keygen` cleanly — the BLE library is pure Go, no
   cgo, no Sailfish Platform SDK needed for this half.
2. As the normal `defaultuser` account, `tesla-control -ble ping` fails with:
   `can't down device: operation not permitted` and suggests
   `setcap 'cap_net_admin=eip'`.
3. After `devel-su setcap 'cap_net_admin=eip' tesla-control`, the same
   unprivileged user gets past HCI init (error becomes `context deadline
   exceeded` against a placeholder VIN with no matching car nearby — i.e.
   the *permission* problem is resolved, confirming the capability grant
   works).
4. `/etc/sailjail/permissions/Base.permission` contains `caps.drop all`,
   applied to every sandboxed profile with no opt-out; the stock
   `Bluetooth.permission` only grants D-Bus access to `org.bluez`, not raw
   HCI. → sandboxed raw HCI is a dead end, hence the two-package split.
5. Sailjail doesn't change the process UID (just capabilities/namespaces via
   firejail), so D-Bus policy keyed on `user="defaultuser"` still applies
   correctly to the sandboxed app — this is what makes the split
   architecture work at all.

## Layout

```
helper/                          Go daemon, cross-compiled for aarch64
  main.go                        D-Bus service: Run/GenerateKey/Pair/SetConfig/GetConfig
  systemd/teslacontrold.service
  dbus/org.teslacontrol.Helper.{service,conf}
  sailjail/TeslaControlHelper.permission
  rpm/teslacontrold.spec
  dist/teslacontrold             built binary (see below)

app/                              Harbour Silica app (qmake project)
  harbour-teslacontrol.pro
  src/teslaclient.{h,cpp}         QDBusInterface client for the helper
  src/harbour-teslacontrol.cpp    main()
  qml/harbour-teslacontrol.qml
  qml/js/CommandCatalog.js        every tesla-control subcommand, grouped like the Tesla app
  qml/pages/                      FirstPage, CategoryPage (generic), ArgumentDialog, PairingPage, SettingsPage
  rpm/harbour-teslacontrol.spec
  harbour-teslacontrol.desktop
  icons/                          placeholder icons - replace before shipping

spike/bin/                        Phase 0 cross-compiled tesla-control/tesla-keygen
```

## Build

### 1. Cross-compile the Go binaries (helper + bundled CLI tools)

No Sailfish SDK needed for this part - pure Go, cross-compiles from any
Linux host with Docker:

```sh
# tesla-control / tesla-keygen (already done once, in spike/bin/):
docker run --rm -v "$PWD/spike/bin:/out" golang:1.23 bash -c '
  git clone --depth 1 https://github.com/teslamotors/vehicle-command.git /src
  cd /src
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/tesla-control ./cmd/tesla-control
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/tesla-keygen ./cmd/tesla-keygen
'

# teslacontrold:
cd helper
docker run --rm -v "$PWD":/src -w /src golang:1.23 \
  env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w" -o dist/teslacontrold .
```

### 2. Build both RPMs with the Sailfish Platform SDK (Docker)

Use a Dockerized `sfdk`/`mb2` build environment targeting
`SailfishOS-5.2.0.16-aarch64` (community images: `sailfishos-open/docker-sailfishos-builder`,
`vranki/sailfishdockersdk`, or Jolla's own SDK with the Docker build-engine
backend). Broad strokes, since the exact image/tag you use will dictate the
precise invocation:

```sh
# teslacontrold.rpm - no compilation, just packaging prebuilt binaries:
mkdir -p helper/rpmbuild/SOURCES
cp helper/dist/teslacontrold spike/bin/tesla-control spike/bin/tesla-keygen \
   helper/systemd/teslacontrold.service \
   helper/dbus/org.teslacontrol.Helper.service helper/dbus/org.teslacontrol.Helper.conf \
   helper/sailjail/TeslaControlHelper.permission \
   helper/rpmbuild/SOURCES/
rpmbuild --target aarch64 --define "_topdir $PWD/helper/rpmbuild" -bb helper/rpm/teslacontrold.spec

# harbour-teslacontrol.rpm - via the Platform SDK (needs Qt5/Silica headers):
tar cjf harbour-teslacontrol-0.1.0.tar.bz2 --transform 's,^app,harbour-teslacontrol-0.1.0,' app
sfdk build --target SailfishOS-5.2.0.16-aarch64 app/rpm/harbour-teslacontrol.spec
```

Both `rpmbuild` invocations above need to actually run inside the Platform
SDK's aarch64 build target (for the right Qt5/glibc ABI), not your host's
generic `rpmbuild` — `sfdk build` handles that for the app; for
`teslacontrold` (no compiled C/C++, just prebuilt static-ish Go binaries +
plain files) a host `rpmbuild --target aarch64` is normally fine since
nothing needs linking against target libraries, but if package validation
complains, run it through `sfdk` the same way.

### 3. Install on the phone

```sh
scp helper/rpmbuild/RPMS/aarch64/teslacontrold-*.rpm defaultuser@<phone-ip>:~/
scp harbour-teslacontrol-0.1.0-1.aarch64.rpm defaultuser@<phone-ip>:~/
ssh defaultuser@<phone-ip>
devel-su pkcon install-local ~/teslacontrold-*.rpm
devel-su pkcon install-local ~/harbour-teslacontrol-*.rpm
systemctl status teslacontrold   # should be active
```

## Testing runbook

1. Launch Tesla Control → pull down → **Settings** → enter VIN → Save.
2. Pull down → **Pair Vehicle** → **Generate Key** → **Pair with Vehicle** →
   tap the NFC card on the center console when the car prompts.
3. Start with read-only commands (Diagnostics → Ping, Keys → List Enrolled
   Keys) before actuation commands (Lock/Unlock, Climate, Trunk).
4. If `teslacontrold` isn't reachable, `FirstPage` shows a banner with the
   install command; check `systemctl status teslacontrold` and
   `journalctl -u teslacontrold` on-device.

## Known gaps / next steps

- `CommandCatalog.js` argument bounds/enum values (STATE on/off, ROLE,
  FORM_FACTOR, `state` CATEGORY names, etc.) are best-effort from public
  docs, not verified against `tesla-control help <cmd>` on this exact
  binary version - check that if a command rejects otherwise-sane input.
- Icons in `app/icons/` are placeholders (solid color + "TC"); replace
  before any real distribution.
- Fleet API / internet mode (`-proxy`, `get`/`post` Fleet API passthrough)
  is intentionally out of scope for v1 - BLE only.
- The Silica app (`app/`) hasn't been compiled against real Qt5/Silica
  headers yet - only syntax-reviewed. Do a `sfdk build` pass and fix
  whatever the compiler flags before relying on it.
