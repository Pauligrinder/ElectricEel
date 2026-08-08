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

Verified working with the `coderus/sailfishos-platform-sdk-aarch64` image
(~13GB, includes a ready `SailfishOS-5.2.0.15-aarch64` mb2/sb2 build
target - one patch version behind the phone's 5.2.0.16, close enough).
Host-container UID mismatch means bind-mounting the source tree directly
doesn't work cleanly - `docker cp` in, build, `docker cp` out:

```sh
docker pull coderus/sailfishos-platform-sdk-aarch64

docker create --name teslacontrol-build coderus/sailfishos-platform-sdk-aarch64 sleep infinity
docker start teslacontrol-build

# --- harbour-teslacontrol.rpm - compiles the C++/QML app for real ---
docker cp app teslacontrol-build:/home/mersdk/app
docker exec -u root teslacontrol-build chown -R mersdk:mersdk /home/mersdk/app
docker exec -w /home/mersdk/app teslacontrol-build \
  mb2 --target SailfishOS-5.2.0.15-aarch64 build
docker cp teslacontrol-build:/home/mersdk/app/RPMS/harbour-teslacontrol-0.1.0-1.aarch64.rpm app/RPMS/

# --- teslacontrold.rpm - no compilation, just packaging prebuilt binaries,
#     but still needs the aarch64 target's rpm/rpmlint config, via sb2 ---
mkdir -p helper/rpmbuild/SOURCES
cp helper/dist/teslacontrold spike/bin/tesla-control spike/bin/tesla-keygen \
   helper/systemd/teslacontrold.service \
   helper/dbus/org.teslacontrol.Helper.service helper/dbus/org.teslacontrol.Helper.conf \
   helper/sailjail/TeslaControlHelper.permission \
   helper/rpmbuild/SOURCES/
docker cp helper teslacontrol-build:/home/mersdk/helper
docker exec -u root teslacontrol-build chown -R mersdk:mersdk /home/mersdk/helper
docker exec -w /home/mersdk/helper teslacontrol-build \
  sb2 -t SailfishOS-5.2.0.15-aarch64 rpmbuild \
    --define "_topdir /home/mersdk/helper/rpmbuild" -bb rpm/teslacontrold.spec
docker cp teslacontrol-build:/home/mersdk/helper/rpmbuild/RPMS/aarch64/teslacontrold-0.1.0-1.aarch64.rpm helper/RPMS/

docker rm -f teslacontrol-build
```

Two gotchas that cost time getting this working, already fixed in the specs
committed here:
- rpmlint's Sailfish config only accepts old Fedora short license names
  (`ASL 2.0`, not `Apache-2.0`) and requires a `%changelog` section -
  both specs use `ASL 2.0` now.
- `teslacontrold`'s prebuilt Go binaries have no GNU build-id note, which
  makes rpm's `find-debuginfo.sh --strict-build-id` hard-fail; its spec
  sets `%global debug_package %{nil}` to skip debuginfo extraction.

Prebuilt RPMs from this exact process are already checked into
`app/RPMS/harbour-teslacontrol-0.1.0-1.aarch64.rpm` and
`helper/RPMS/teslacontrold-0.1.0-1.aarch64.rpm`.

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
   `journalctl -u teslacontrold` on-device. If the daemon's journal shows
   calls being authorized but the banner still won't clear, check the
   app's own log too (`journalctl -t harbour-teslacontrol`) for a QML
   error - see [KNOWN_ISSUES.md](KNOWN_ISSUES.md) for one already found
   and fixed this way.
5. The launcher runs the app with `--single-instance`: backgrounding it
   via the multitasking view and relaunching from the app grid resumes
   the existing process rather than starting fresh, so `Component.onCompleted`
   won't re-run and any newly-deployed QML won't take effect. Kill it
   explicitly when iterating: `devel-su pkill -f harbour-teslacontrol`.

## Build verification status

Both RPMs have been built for real (not just written) using
`coderus/sailfishos-platform-sdk-aarch64` with its bundled
`SailfishOS-5.2.0.15-aarch64` target - one version patch behind the phone's
5.2.0.16, close enough to compile/link against:

- **`harbour-teslacontrol`**: `mb2 --target SailfishOS-5.2.0.15-aarch64 build`
  compiled the C++, ran moc, linked against real Qt5/Silica/DBus, and
  packaged the RPM cleanly (0 rpmlint errors after fixing the license tag
  format and adding `%changelog` - see below). QML files aren't compiled,
  only reviewed, so runtime QML errors are still possible.
- **`teslacontrold`**: packaged with `rpmbuild` (via `sb2 -t
  SailfishOS-5.2.0.15-aarch64`) after disabling debuginfo extraction
  (`%global debug_package %{nil}`), since prebuilt Go binaries carry no
  GNU build-id note and `find-debuginfo.sh --strict-build-id` errored on
  them otherwise. Systemd scriptlet macros expanded correctly.

Both `.rpm` files are checked into `app/RPMS/` and `helper/RPMS/` respectively.
Neither has been installed on the phone yet or tested against a real vehicle.

## Security notes

`teslacontrold`'s D-Bus methods (`Run`, `GenerateKey`, `Pair`, `SetConfig`,
`GetConfig`) check the calling process's PID/UID via
`org.freedesktop.DBus.GetConnectionCredentials` and `/proc/<pid>/exe` against
an allow-list (`TESLACONTROLD_ALLOWED_CALLERS`, default
`/usr/bin/harbour-teslacontrol,/usr/bin/xdg-dbus-proxy`) before doing
anything, since the D-Bus system policy alone can only scope access to the
`defaultuser` account, not to a specific app.

**Verified on real hardware (Jolla Phone 2026, Sailfish 5.2.0.16):** Sailjail
routes a sandboxed app's entire system-bus traffic through a dedicated
per-app `xdg-dbus-proxy` process, so the caller PID/exe that
`teslacontrold` resolves for a legitimate call is the proxy's
(`/usr/bin/xdg-dbus-proxy`), not `harbour-teslacontrol`'s own - hence
that binary being in the allow-list default. The actual per-app gate is
Sailjail itself: only an app whose `.desktop` file declares the
`TeslaControlHelper` permission gets `org.teslacontrol.Helper` added to
its own proxy's filter at all. `teslacontrold`'s allow-list still blocks a
rogue *unsandboxed* process calling directly as `defaultuser`, since such
a process's own exe matches neither allow-list entry. Resolving the
proxy's exe cross-UID also requires `CAP_SYS_PTRACE`, granted via
`AmbientCapabilities` in `teslacontrold.service` - without it, every
caller is silently rejected (`Forbidden: cannot resolve caller binary`),
which is what "helper service not found" turned out to mean the first
time this was tested end-to-end. See [KNOWN_ISSUES.md](KNOWN_ISSUES.md)
for the full investigation and why the previously-proposed SMACK-label
fallback doesn't apply on this device.

## Known gaps / next steps

- `CommandCatalog.js` argument bounds/enum values (STATE on/off, ROLE,
  FORM_FACTOR, `state` CATEGORY names, etc.) are best-effort from public
  docs, not verified against `tesla-control help <cmd>` on this exact
  binary version - check that if a command rejects otherwise-sane input.
- Icons in `app/icons/` are placeholders (solid color + "TC"); replace
  before any real distribution.
- Fleet API / internet mode (`-proxy`, `get`/`post` Fleet API passthrough)
  is intentionally out of scope for v1 - BLE only.
- Compiled and packaged, but **not yet installed or run on-device** - the
  `%pre`/`%post` scriptlets (user/group creation, `setcap`, systemd enable)
  are only syntax-checked by rpmbuild, not execution-tested; same for the
  actual D-Bus round-trip between the app and the helper.
- Built RPMs and rpmbuild staging output are no longer tracked in git (see
  `.gitignore`) - they're build output, not source, and get regenerated by
  the steps above. Rebuild locally before installing rather than relying on
  a committed copy.
- See [KNOWN_ISSUES.md](KNOWN_ISSUES.md) for the full list, including a
  D-Bus caller-authorization fix from a later review pass that still needs
  on-device verification.
