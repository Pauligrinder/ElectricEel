# Known issues / what a "prod quality" pass could not fully close

This file tracks gaps found during a code review pass (2026-08-07) that
were either fixed, or documented here because closing them needs
something this environment doesn't have: a real Sailfish device, the
Platform SDK, or a car to pair against.

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
