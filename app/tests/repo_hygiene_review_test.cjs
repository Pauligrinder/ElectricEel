const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

// Build / release / repository-hygiene review. Each test encodes the desired
// state of the repository tooling (not application logic) and its comment
// records the finding that motivated it. Tests marked "guard" protect an
// invariant that already held when the review was written.

const repoRoot = path.join(__dirname, "../..");
const read = (rel) => fs.readFileSync(path.join(repoRoot, rel), "utf8");
const exists = (rel) => fs.existsSync(path.join(repoRoot, rel));

let trackedCache;
function trackedFiles() {
    if (trackedCache !== undefined) return trackedCache;
    try {
        trackedCache = execFileSync("git", ["ls-files", "-z"], { cwd: repoRoot, encoding: "utf8" })
            .split("\0")
            .filter(Boolean);
    } catch (_) {
        trackedCache = null; // not a git checkout (e.g. source tarball)
    }
    return trackedCache;
}

const workflowDir = ".github/workflows";
const workflows = fs.readdirSync(path.join(repoRoot, workflowDir))
    .filter((f) => /\.ya?ml$/.test(f))
    .map((f) => ({ name: `${workflowDir}/${f}`, text: read(`${workflowDir}/${f}`) }));
const ciYml = read(`${workflowDir}/ci.yml`);
const releaseYml = read(`${workflowDir}/release.yml`);
const upstreamYml = read(`${workflowDir}/upstream-version.yml`);

// Drop shell/YAML comment lines so documentation comments do not count as
// executed commands.
const codeLines = (text) => text.split("\n").filter((l) => !/^\s*#/.test(l));

// ---------------------------------------------------------------------------
// Tracked build output
// ---------------------------------------------------------------------------

// helper/session/teslacontrol-session is an 18 MB x86-64 Go ELF (with debug
// info, built from the pre-FFI child process) committed in 2ba68ae; it is not
// used by any build, cannot be verified from source, and bloats every clone
// forever. tools/__pycache__/*.pyc is interpreter cache. docs/build.md even
// claims "no binaries are committed". Fix: git rm --cached both, purge from
// history if size matters, and ignore them (see next test).
test("no compiled binaries or bytecode are tracked in git", (t) => {
    const files = trackedFiles();
    if (!files) return t.skip("git not available");
    const offenders = files.filter((rel) => {
        if (/(^|\/)__pycache__\/|\.(pyc|pyo|o|a|so|dll|exe)$/.test(rel)) return true;
        const full = path.join(repoRoot, rel);
        let fd;
        try {
            fd = fs.openSync(full, "r");
            const buf = Buffer.alloc(4);
            fs.readSync(fd, buf, 0, 4, 0);
            return buf.equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46])); // \x7fELF
        } catch (_) {
            return false;
        } finally {
            if (fd !== undefined) fs.closeSync(fd);
        }
    });
    assert.deepEqual(offenders, []);
});

// .gitignore names /helper/session/electric-eel-session and tesla-session but
// not the binary actually produced/committed (teslacontrol-session), and has
// no Python bytecode rule, which is how the two blobs above got committed.
test(".gitignore covers python bytecode and the legacy Go session binary", (t) => {
    if (!trackedFiles()) return t.skip("git not available");
    for (const probe of ["tools/__pycache__/x.cpython-314.pyc", "helper/session/teslacontrol-session"]) {
        let ignored = true;
        try {
            execFileSync("git", ["check-ignore", "-q", "--no-index", probe], { cwd: repoRoot });
        } catch (_) {
            ignored = false;
        }
        assert.ok(ignored, `${probe} is not ignored`);
    }
});

// ---------------------------------------------------------------------------
// Dangling references in tooling / docs
// ---------------------------------------------------------------------------

// .gitignore cites KNOWN_ISSUES.md, release.yml and make-app-bundle.sh cite
// BLUEZ_BACKEND_PLAN.md, docs/tesla-ble-abi.md cites
// docs/go-c-abi-migration-plan.md, go.mod cites
// thirdparty/vehicle-command/README.electriceel.md (the content lives in
// docs/vehicle-command-patch.md). None of these files exist.
test("tooling and docs only reference markdown files that exist", (t) => {
    const files = trackedFiles();
    if (!files) return t.skip("git not available");
    const basenames = new Set(files.map((f) => path.basename(f)));
    const scanned = [".gitignore", "build.sh", "helper/make-app-bundle.sh",
        "helper/build-go-library-sdk.sh", "helper/session/go.mod", "README.md",
        ...workflows.map((w) => w.name),
        ...files.filter((f) => /^docs\/[^/]+\.md$/.test(f))];
    const missing = [];
    for (const rel of scanned) {
        for (const m of read(rel).matchAll(/[A-Za-z0-9_./-]+\.md\b/g)) {
            if (!basenames.has(path.basename(m[0]))) missing.push(`${rel} -> ${m[0]}`);
        }
    }
    assert.deepEqual([...new Set(missing)], []);
});

// ---------------------------------------------------------------------------
// Versioning
// ---------------------------------------------------------------------------

const proVersion = read("app/harbour-electric-eel.pro").match(/^VERSION\s*=\s*(\S+)/m)[1];
const cargoVersion = read("helper/Cargo.toml").match(/^version\s*=\s*"([^"]+)"/m)[1];
const lockVersion = read("helper/Cargo.lock")
    .match(/name = "electric-eel-core"\nversion = "([^"]+)"/)[1];
const spec = read("app/rpm/harbour-electric-eel.spec");
const specVersion = spec.match(/^Version:\s*(\S+)/m)[1];
const changelog = [...spec.matchAll(/^\* .* - (\S+)\s*$/gm)].map((m) => m[1]);

// guard: the Settings page compares app (.pro) and core (Cargo) versions, and
// the RPM version must be the same; the newest changelog entry must describe
// the version being built (rpmlint: incoherent-version-in-changelog).
test("guard: .pro, Cargo.toml, Cargo.lock, spec and changelog versions agree", () => {
    assert.equal(cargoVersion, proVersion, "Cargo.toml vs .pro");
    assert.equal(lockVersion, proVersion, "Cargo.lock vs .pro");
    assert.equal(specVersion, proVersion, "spec Version vs .pro");
    assert.equal(changelog[0], `${specVersion}-1`, "newest %changelog entry");
});

// The spec %changelog has two "- 0.3.2-1" entries (Pauli Kettunen and Marco
// Napetti, both Tue Oct 13 2026). A changelog entry identifies one
// version-release; merge them into a single entry.
test("spec %changelog has exactly one entry per version-release", () => {
    const dupes = changelog.filter((v, i) => changelog.indexOf(v) !== i);
    assert.deepEqual(dupes, []);
});

// release.yml sed-rewrites VERSION in the .pro, Cargo.toml and the spec from
// the tag instead of verifying them. Committed versions can therefore drift
// silently: tag v0.2.13 shipped from a tree whose spec/.pro say 0.2.12 and
// whose %changelog has no 0.2.13 entry (v0.2.2/v0.2.3 likewise). It also
// forces the build to rewrite Cargo.lock, which is why no cargo invocation can
// use --locked. Fix: fail the release when the tag != committed versions.
test("release workflow verifies tag == committed versions instead of rewriting files", () => {
    const rewrites = codeLines(releaseYml).filter((l) =>
        /sed -i/.test(l) && /(harbour-electric-eel\.pro|Cargo\.toml|\.spec)/.test(l));
    assert.deepEqual(rewrites, []);
});

// ---------------------------------------------------------------------------
// Test coverage in CI
// ---------------------------------------------------------------------------

// app/tests/phonekeyevents_test.cpp (D-Bus publisher of PhoneKeyEvents.qml)
// and app/tests/rustruntime_test.cpp (real Rust entrypoint + Qt adapter + Go
// library) are documented in docs/phone-key-events.md / docs/build.md but no
// workflow builds or runs them, so they rot unnoticed.
test("every Qt C++ test under app/tests is built by a CI workflow", () => {
    const cppTests = fs.readdirSync(__dirname).filter((f) => /_test\.cpp$/.test(f));
    const allWorkflows = workflows.map((w) => w.text).join("\n");
    const unbuilt = cppTests.filter((f) =>
        !allWorkflows.includes(f) && !allWorkflows.includes(f.replace(/_test\.cpp$/, ".pro")));
    assert.deepEqual(unbuilt, []);
});

// The shipped staticlib is built with `--features app-entry` (no `dbus`).
// CI only runs clippy/test with --all-features, which enables `dbus`, and the
// lib has many #[cfg(not(any(test, feature = "dbus")))] paths plus
// #[cfg(all(feature = "app-entry", not(test)))] -- code that ships but is never
// linted (clippy pedantic -D warnings) on the host. docs/build.md and
// docs/tesla-ble-abi.md document exactly this command; CI does not run it.
test("CI lints the shipped Rust feature set (app-entry without dbus)", () => {
    const lines = codeLines(ciYml).filter((l) => /cargo (clippy|check|build)/.test(l));
    assert.ok(lines.some((l) => /--features[ =]app-entry/.test(l) && !/--all-features/.test(l)),
        "no cargo clippy/check --features app-entry step in ci.yml");
});

// The Rust<->Go FFI integration tests (tesla_ble.rs, protocol_client.rs) are
// #[ignore = "requires the built Go shared library"] and helper/session/abi/
// smoke.c is a C ABI smoke test; docs/tesla-ble-abi.md documents both as
// host-runnable, but CI never builds libteslable.so on the host, so the
// cross-language contract is only exercised by hand.
test("CI runs the ignored Rust/Go integration tests and the C ABI smoke test", () => {
    const code = codeLines(ciYml).join("\n");
    assert.ok(/TESLA_BLE_TEST_LIBRARY/.test(code), "ci.yml never sets TESLA_BLE_TEST_LIBRARY");
    assert.ok(/--include-ignored|--ignored/.test(code), "ci.yml never runs ignored Rust tests");
    assert.ok(/abi\/smoke\.c/.test(code), "ci.yml never builds abi/smoke.c");
});

// helper/session/thirdparty/vehicle-command is a separate Go module (own
// go.mod), so `go test ./...` in helper/session never enters it. The local
// patches (navigation_ee.go + its byte-exact navigation_ee_test.go,
// SessionInfo receiver fix, key.go) are therefore untested in CI.
test("CI tests the patched vendored vehicle-command module", () => {
    const code = codeLines(ciYml).join("\n");
    assert.ok(/thirdparty\/vehicle-command[\s\S]{0,200}go test|go test[^\n]*thirdparty\/vehicle-command/.test(code),
        "ci.yml never runs go test in helper/session/thirdparty/vehicle-command");
});

// ---------------------------------------------------------------------------
// Reproducibility
// ---------------------------------------------------------------------------

// Cargo.lock is committed but no cargo invocation in CI, release or
// make-app-bundle.sh passes --locked, so the shipped staticlib can be built
// with dependency versions nobody reviewed (the lockfile is silently
// rewritten). `cargo clippy --locked --lib --features app-entry` passes today.
test("every cargo build/test/clippy invocation uses --locked", () => {
    const sources = [...workflows, { name: "helper/make-app-bundle.sh", text: read("helper/make-app-bundle.sh") }];
    const unlocked = [];
    for (const { name, text } of sources) {
        for (const l of codeLines(text)) {
            if (/\bcargo (build|test|clippy|check|run)\b/.test(l) && !/--locked|--frozen/.test(l)) {
                unlocked.push(`${name}: ${l.trim()}`);
            }
        }
    }
    assert.deepEqual(unlocked, []);
});

// Cargo.toml sets [lints.clippy] pedantic = "warn" and CI runs clippy with
// -D warnings on dtolnay/rust-toolchain@stable: every Rust release that adds
// or tightens a pedantic lint turns CI red with no code change, and release
// artifacts are built with whatever compiler is current that day.
test("Rust toolchain is pinned (rust-toolchain.toml or explicit version)", () => {
    const pinnedFile = exists("helper/rust-toolchain.toml") || exists("rust-toolchain.toml")
        || exists("helper/rust-toolchain") || exists("rust-toolchain");
    const floating = workflows.filter((w) => /dtolnay\/rust-toolchain@(stable|beta|nightly)\b/.test(w.text)
        && !/toolchain:\s*['"]?\d+\.\d+/.test(w.text)).map((w) => w.name);
    assert.ok(pinnedFile || floating.length === 0, `floating toolchain in ${floating.join(", ")}`);
});

// `coderus/sailfishos-platform-sdk-aarch64` is pulled with no tag or digest
// in ci.yml, release.yml, build.sh and helper/build-go-library-sdk.sh, while
// the target name SailfishOS-5.2.0.15-aarch64 is hard-coded. A new :latest
// push can change the toolchain under a release or drop the 5.2.0.15 target
// and break every build. It is also an unverified ~13 GB third-party image
// that compiles the shipped binaries. Pin by @sha256 digest (or at least a
// version tag) in one place.
test("Sailfish SDK image is pinned by tag or digest", () => {
    const sources = [...workflows.map((w) => w.name), "build.sh", "helper/build-go-library-sdk.sh"];
    const unpinned = [];
    for (const rel of sources) {
        for (const l of codeLines(read(rel))) {
            if (/coderus\/sailfishos-platform-sdk-aarch64(?![:@\w-])/.test(l)) unpinned.push(`${rel}: ${l.trim()}`);
        }
    }
    assert.deepEqual(unpinned, []);
});

// guard: Go is pinned in three workflows; it must match go.mod's go directive
// (better: use `go-version-file: helper/session/go.mod`).
test("guard: workflow go-version matches helper/session/go.mod", () => {
    const goMod = read("helper/session/go.mod").match(/^go (\S+)/m)[1];
    for (const w of workflows) {
        for (const m of w.text.matchAll(/go-version:\s*['"]?([\d.]+)/g)) {
            assert.equal(m[1], goMod, `${w.name} go-version`);
        }
    }
});

// tools/build-qm.sh runs `pip download PySide6-Essentials` with no version,
// and the CI i18n gate fails on any byte difference in the regenerated
// .ts/.qm. A new PySide6/Qt release that changes lupdate/lrelease output
// turns CI red with no source change. It also downloads into a fixed
// /tmp/qttools-wheel and picks glob(...)[0], so a stale wheel can be used.
test("tools/build-qm.sh pins the PySide6-Essentials version", () => {
    assert.match(read("tools/build-qm.sh"), /PySide6-Essentials==\d+\.\d+/);
});

// ---------------------------------------------------------------------------
// Workflow security
// ---------------------------------------------------------------------------

// release.yml grants `contents: write` at workflow level, so every step --
// cargo build scripts, Go module downloads, the unpinned SDK container, and
// tag-pinned third-party actions -- runs with a token that can rewrite
// releases and tags. Default to read; grant write only to a separate publish
// job that consumes the built RPM as an artifact.
test("release workflow does not grant contents: write at workflow level", () => {
    const topLevel = releaseYml.split(/^jobs:/m)[0];
    assert.doesNotMatch(topLevel, /contents:\s*write/);
});

// All `uses:` are pinned by mutable tags (actions/checkout@v4,
// softprops/action-gh-release@v2, dtolnay/rust-toolchain@stable ...). A
// retagged/compromised action runs with the release write token. Pin by full
// commit SHA (with a version comment) and let Dependabot bump them.
test("third-party actions are pinned by full commit SHA", () => {
    const unpinned = [];
    for (const w of workflows) {
        for (const m of w.text.matchAll(/^\s*-?\s*uses:\s*([^\s#]+)/gm)) {
            if (m[1].startsWith("./")) continue;
            if (!/@[0-9a-f]{40}$/.test(m[1])) unpinned.push(`${w.name}: ${m[1]}`);
        }
    }
    assert.deepEqual(unpinned, []);
});

// upstream-version.yml fails on every push and pull_request as soon as
// teslamotors tags a new vehicle-command release, so unrelated commits go red
// until someone rebases the vendored patch. A freshness signal is not a
// correctness gate: run it on schedule/workflow_dispatch only (optionally
// opening an issue).
test("upstream freshness check does not gate pushes and pull requests", () => {
    const on = upstreamYml.split(/^permissions:|^jobs:/m)[0];
    assert.ok(!/^\s{2}(push|pull_request):/m.test(on), "upstream-version.yml triggers on push/pull_request");
});

// ---------------------------------------------------------------------------
// Translation tooling and catalogs
// ---------------------------------------------------------------------------

// tools/fill-{a..g}.py (~964 lines each) are one-shot, positional translation
// dumps. fill_common.py asserts each list matches the template order of the
// day ("156 messages"); the template now has 180, so none of them can run, and
// if one were adapted it would overwrite every newer translation of its
// languages (write_all regenerates the whole file from the template). Batches
// also overlap (fill-a and fill-b both write es/pt/pt_BR/nl; fill-f and
// fill-g both write ml/mr/pa). The .ts files are the source of truth; delete
// the scripts (git history keeps them).
test("stale positional translation dump scripts are not tracked", (t) => {
    const files = trackedFiles();
    if (!files) return t.skip("git not available");
    const template = read("app/translations/harbour-electric-eel.ts");
    const templateCount = (template.match(/<message>/g) || []).length;
    const stale = files.filter((f) => /^tools\/fill-[a-g]\.py$/.test(f));
    assert.deepEqual(stale, [], `positional scripts written for 156 messages; template has ${templateCount}`);
});

// guard: every language catalog is listed in the .pro TRANSLATIONS, has a
// compiled .qm, declares the matching language, has no unfinished entries,
// and the count matches the "39 languages" claim in README/docs.
test("guard: translation catalogs are complete and consistent", () => {
    const dir = path.join(repoRoot, "app/translations");
    const pro = read("app/harbour-electric-eel.pro");
    const langs = fs.readdirSync(dir)
        .map((f) => f.match(/^harbour-electric-eel_(.+)\.ts$/))
        .filter(Boolean)
        .map((m) => m[1]);
    for (const lang of langs) {
        const ts = fs.readFileSync(path.join(dir, `harbour-electric-eel_${lang}.ts`), "utf8");
        assert.ok(pro.includes(`translations/harbour-electric-eel_${lang}.ts`), `${lang} missing from .pro`);
        assert.ok(fs.existsSync(path.join(dir, `harbour-electric-eel_${lang}.qm`)), `${lang}.qm missing`);
        assert.match(ts, new RegExp(`<TS [^>]*language="${lang}"`), `${lang} language attribute`);
        assert.doesNotMatch(ts, /type="(unfinished|vanished|obsolete)"/, `${lang} has unfinished entries`);
    }
    assert.match(read("README.md"), new RegExp(`${langs.length} languages`));
});
