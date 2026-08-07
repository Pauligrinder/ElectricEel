# Known issues / what a "prod quality" pass could not fully close

This file tracks gaps found during a code review pass (2026-08-07) that
were either fixed, or documented here because closing them needs
something this environment doesn't have: a real Sailfish device, the
Platform SDK, or a car to pair against.

## Fixed in this pass

- **D-Bus caller authorization** (`helper/main.go`): `Run`, `GenerateKey`,
  `Pair`, `SetConfig`, and `GetConfig` now resolve the calling process's
  PID via `org.freedesktop.DBus.GetConnectionUnixProcessID` and check
  `/proc/<pid>/exe` against an allow-list (`TESLACONTROLD_ALLOWED_CALLERS`,
  default `/usr/bin/harbour-teslacontrol`), denying by default on any
  lookup failure. Previously the D-Bus system policy
  (`org.teslacontrol.Helper.conf`) only scoped access to the `defaultuser`
  Unix account - Sailfish being single-user, that meant *any* process
  running as `defaultuser` could call these methods directly (unlock,
  honk, open trunk, valet mode...), not just `harbour-teslacontrol`,
  since the Sailjail `.permission` file only filters D-Bus for apps
  launched *through* the sandbox, not arbitrary unsandboxed processes.
  **This closes the gap for unsandboxed rogue processes/scripts running
  as `defaultuser`.** See the unresolved caveat below - it does not
  necessarily close it for other *sandboxed* apps.
- Repo hygiene: prebuilt binaries, built RPMs, rpmbuild staging output,
  and build logs are no longer tracked in git (`.gitignore` added); they
  remain on disk locally where useful (`spike/bin/`, `helper/dist/`,
  `*/RPMS/`) so existing local builds aren't lost.

## Unresolved - needs on-device verification

- **Whether the new caller check actually distinguishes sandboxed apps.**
  Sailjail sandboxes apps with firejail, and depending on the exact
  Sailfish OS version, sandboxed apps' system-bus traffic may be routed
  through a shared `xdg-dbus-proxy` process rather than connecting to the
  system bus directly. If that's the case here, `GetConnectionUnixProcessID`
  would resolve to the proxy's PID/exe (the same for every sandboxed app),
  not `harbour-teslacontrol`'s own PID - which would mean the allow-list
  check either needs to include the proxy's path (weakening the check
  back to "any sandboxed app", not just this one) or a different
  mechanism entirely (e.g. checking the SMACK security label via
  `org.freedesktop.DBus.GetConnectionCredentials`'s `LinuxSecurityLabel`
  field, which Sailfish's `dbus-daemon` is known to support and which
  *is* per-app). I could not verify which applies without a real device.
  **Action before relying on this**: install both packages, run
  `journalctl -u teslacontrold`, and trigger a command from the app. If
  you see a `"rejected call from pid ... not in
  TESLACONTROLD_ALLOWED_CALLERS"` log line even for legitimate in-app
  taps, capture the logged exe path and either add it to
  `TESLACONTROLD_ALLOWED_CALLERS` or switch to a SMACK-label check.
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
