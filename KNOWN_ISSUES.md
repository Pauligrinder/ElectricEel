# Known issues

Genuinely open items only. Resolved bugs and completed migrations aren't
tracked here — see `git log` if you need that history.

- **Cross-page reply collisions in the UI** (`app/qml/pages/`):
  `CategoryPage.qml`'s "keys" category and `PairingPage.qml` both issue
  `runCommand("list-keys", "list-keys", [])` — i.e. the same `requestId`.
  Sailfish's `PageStack` keeps prior pages alive (not destroyed) after
  navigating forward, so if both pages are simultaneously present in the
  stack, both pages' `Connections` blocks match the same reply and both
  update their own result label, even though only one triggered the call.
  Harmless (no crash, no wrong data — both show the same real result), but
  not correct. Fixing properly means giving every call a unique
  `requestId` (e.g. a counter or uuid) instead of the command name, which
  needs the app running to confirm `PageStack` lifecycle assumptions.

- **`CommandCatalog.js` argument definitions are shared, mutable
  singletons.** `.pragma library` means `CATEGORIES` is created once and
  reused for the life of the app; `ArgumentDialog.qml` writes the user's
  input onto the same `arg()` object (`argSpec.__value = ...`) rather than
  a copy. In practice this doesn't currently cause visibly wrong behavior,
  because every field type re-derives its value from the catalog's
  `def`/`min`/`max` on each dialog open (`Component.onCompleted` always
  overwrites `__value`), but it's fragile: any future field type that
  doesn't re-sync on load would silently leak state between unrelated
  dialog opens. Worth cloning `commandDef.args` before passing it to
  `ArgumentDialog.qml` in `CategoryPage.qml` if this file changes.

- **`CommandCatalog.js` argument bounds/enum values** (`STATE` on/off,
  `ROLE`, `FORM_FACTOR`, `state` `CATEGORY` names, etc.) are best-effort
  from public docs, not verified against `tesla-control help <cmd>` on the
  exact upstream binary version in use. Check this first if a command
  rejects otherwise-sane input.

- **`spike/bin/tesla-control` and `spike/bin/tesla-keygen`** are prebuilt
  binaries built once via Docker from `teslamotors/vehicle-command`'s
  default branch (no commit pin). Kept locally (gitignored, not
  committed) for convenience and for the legacy raw-HCI (`hci`)
  transport's diagnostics only — not packaged in the app RPM. If used
  manually for an `hci`-mode test, rebuild fresh from a pinned upstream
  tag rather than trusting the checked-out copy (they also need
  `setcap cap_net_admin=eip`).
