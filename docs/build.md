# Build and release

## Stage the in-app bundle

`helper/make-app-bundle.sh` cross-builds the Rust staticlib with `app-entry`
(Rust application entrypoint, autonomous runtime, and MCE client)
(`aarch64-unknown-linux-gnu`, glibc, `cargo build --locked`) and the Go
`libteslable.so` protocol library (`CGO_ENABLED=1`, SDK target compiler),
staging them into `app/thirdparty/` and `app/lib/`. Requires rustup, Go and
Docker with the Sailfish image. Re-run whenever `helper/` changes; nothing under
`app/thirdparty` or `app/lib` is committed, and no binaries are tracked in git.
`tools/build-qm.sh` refreshes the translation catalogs.

## Pinned toolchains

| What | Pinned in |
| --- | --- |
| Rust compiler, clippy, rustfmt, aarch64 target | `helper/rust-toolchain.toml` (rustup installs it on first use; `rust-version` in `helper/Cargo.toml` matches) |
| Rust dependencies | `helper/Cargo.lock` (every cargo invocation uses `--locked`) |
| Go | `helper/session/go.mod` (`go` directive; workflows use `go-version-file`) |
| Sailfish SDK image | `coderus/sailfishos-platform-sdk-aarch64:5.2.0.15@sha256:…` in `.github/workflows/{ci,release}.yml`, `build.sh` and `helper/build-go-library-sdk.sh` (override with `SAILFISH_SDK_IMAGE`) |
| Qt linguist tools | `PySide6-Essentials` version in `tools/build-qm.sh` |
| GitHub Actions | full commit SHA with a version comment; Dependabot (`.github/dependabot.yml`) proposes updates |

Bump a pin deliberately in its own change: the compiler, clippy and lupdate can
change CI results with no source change.

## Build the RPM (Sailfish Platform SDK, Docker)

The `coderus/sailfishos-platform-sdk-aarch64` image (~13 GB) provides
the `SailfishOS-5.2.0.15-aarch64` target. Host-container UID mismatch
means bind-mounting does not work; copy in and out:

`./build.sh` automates the steps below (and bumps a local RPM release
counter). By hand:

```sh
image=coderus/sailfishos-platform-sdk-aarch64:5.2.0.15@sha256:e5f7596d14502746b308c3dde36f65fc25d7f76c45a01acaed56a1f55b76f592
docker pull "$image"

docker create --name electric-eel-build "$image" sleep infinity
docker start electric-eel-build

docker cp app electric-eel-build:/home/mersdk/app
docker exec -u root electric-eel-build chown -R mersdk:mersdk /home/mersdk/app
docker exec -w /home/mersdk/app electric-eel-build \
  mb2 --target SailfishOS-5.2.0.15-aarch64 build
docker cp electric-eel-build:/home/mersdk/app/RPMS/harbour-electric-eel-<version>-1.aarch64.rpm app/RPMS/

docker rm -f electric-eel-build
```

rpmlint's Sailfish config accepts only old Fedora short license names
(`ASL 2.0`, not `Apache-2.0`) and requires a `%changelog` section. The
private Go library is non-executable (0644) under the app's `lib/` directory;
both final ELF files are stripped. The SDK 5.2 Harbour validator passes the
migrated RPM, including Libraries, Permissions and Requires.
QML files are not compiled by `mb2`, only reviewed.

## Runtime checks

Rust tests include a private-bus MCE integration test (requires `dbus-daemon`)
and verify autonomous retry deadlines and notification detachment:

```sh
cargo test --locked --manifest-path helper/Cargo.toml --lib --features runtime
cargo clippy --locked --manifest-path helper/Cargo.toml --lib --features app-entry -- -D warnings
```

`helper/build.rs` regenerates `helper/electriceelcore.h` (cbindgen) on every
build; commit it together with FFI changes. CI fails when the committed header
differs from the generated one.

After staging the bundle and copying `app/` into the SDK, test the real Rust
entrypoint/runtime against the Qt adapter without loading a Sailfish view:

The SDK needs `pkgconfig(Qt5Test)` (`qt5-qttest-devel`) for this test. It is a
test-only dependency, not a runtime dependency of the RPM.

```sh
docker exec electric-eel-build \
  sb2 -t SailfishOS-5.2.0.15-aarch64 -m sdk-install -R zypper --non-interactive in qt5-qttest-devel
docker exec -w /home/mersdk/app/tests electric-eel-build \
  sb2 -t SailfishOS-5.2.0.15-aarch64 qmake rustruntime.pro
docker exec -w /home/mersdk/app/tests electric-eel-build \
  sb2 -t SailfishOS-5.2.0.15-aarch64 make
docker exec -w /home/mersdk/app/tests electric-eel-build \
  sb2 -t SailfishOS-5.2.0.15-aarch64 ./rustruntime-test
```

This checks action/result marshaling, GUI-thread delivery, and Rust's delayed
refresh while the GUI thread is sleeping. The only production QML timer is
the dashboard label-age update; backend deadlines do not depend on Qt.

The test also loads the real Go shared library alongside Qt/Rust and verifies
asynchronous callbacks and shutdown. Copy `helper/session/abi` to the matching
`/home/mersdk/helper/session/abi` location in the SDK before building the test.
CI's `harbour` job runs this test and `phonekeyevents-test`
([phone-key-events.md](phone-key-events.md)) under `dbus-run-session` after the
Harbour check.

Native ABI checks and host integration commands are in [tesla-ble-abi.md](tesla-ble-abi.md).
Run Harbour validation on the final RPM with:

```sh
docker exec -w /home/mersdk/app electric-eel-build \
  mb2 --target SailfishOS-5.2.0.15-aarch64 check --suites harbour
```

CI builds and Harbour-checks the aarch64 RPM on every push and pull request,
independently of the faster host checks (Rust fmt/clippy/tests, Rust/Go FFI
integration and C smoke test, Go tests including the patched
`thirdparty/vehicle-command` module, QML lint, Qt and node tests, translation
freshness). Release builds run the same Harbour check before publishing. Library runtime requirements are generated by RPM rather
than explicit `qt5-*` package requirements; the Automagic export uses only the
existing Documents permission.

## Install on the phone

One package (`devel-su` only for `pkcon` itself):

```sh
scp app/RPMS/harbour-electric-eel-<version>-1.aarch64.rpm defaultuser@<phone-ip>:/tmp/
ssh defaultuser@<phone-ip>
devel-su pkcon install-local /tmp/harbour-electric-eel-<version>-1.aarch64.rpm
```

After updating, kill the running instance once (`pkill -f
harbour-electric-eel` as `defaultuser`, then relaunch from the grid):
the launcher uses `--single-instance`, so a backgrounded process would
otherwise keep running the old QML.

## Release (GitHub CI)

`.github/workflows/release.yml` builds the RPM and publishes it. Versions are
committed, never stamped by CI. To release:

1. Bump `VERSION` in `app/harbour-electric-eel.pro`, `version` in
   `helper/Cargo.toml` (then `cargo update --workspace --manifest-path
   helper/Cargo.toml` to refresh `Cargo.lock`), `Version:` in the spec, and add
   a `%changelog` entry `<version>-1` at the top of the spec.
2. Commit, then tag that commit and push the tag:

```sh
git tag v<version>
git push origin v<version>
```

The build job fails unless the tag (leading `v` stripped) equals all of these
committed versions, so the Settings page versions and `core_version()` always
match the release. It runs with read-only repository access, rebuilds the
bundle (staticlib + `libteslable.so`) on the runner, Harbour-checks the RPM and
uploads it with `SHA256SUMS` as a workflow artifact. A separate publish job,
the only one with `contents: write`, attaches those files to the tag's GitHub
release. A manual `workflow_dispatch` run builds the committed version and
only uploads the artifact.
