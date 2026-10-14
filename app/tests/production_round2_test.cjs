const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { loadFunctions, runHandler } = require("./qml_helpers.cjs");

// Production review round 2: each test encodes the DESIRED behavior.
// All tests FAIL on the current code and PASS once the fix lands.
// They execute the real QML/JS/Rust sources (no copies of the buggy logic).

const qmlRoot = path.join(__dirname, "../qml");
const firstPageSource = fs.readFileSync(path.join(qmlRoot, "pages/FirstPage.qml"), "utf8");
const categorySource = fs.readFileSync(path.join(qmlRoot, "pages/CategoryPage.qml"), "utf8");
const navigationSource = fs.readFileSync(path.join(qmlRoot, "pages/NavigationPage.qml"), "utf8");
const pairingSource = fs.readFileSync(path.join(qmlRoot, "pages/PairingPage.qml"), "utf8");
const coverSource = fs.readFileSync(path.join(qmlRoot, "cover/CoverPage.qml"), "utf8");
const dialogSource = fs.readFileSync(path.join(qmlRoot, "pages/ArgumentDialog.qml"), "utf8");
const vehicleStateSource = fs.readFileSync(path.join(qmlRoot, "js/VehicleState.js"), "utf8");
const catalogSource = fs.readFileSync(path.join(qmlRoot, "js/CommandCatalog.js"), "utf8");

function loadVehicleState(extra = {}) {
    const ctx = vm.createContext({ qsTr: (s) => s, console, Date, Math, ...extra });
    vm.runInContext(vehicleStateSource.replace(/^\.pragma library\s*$/m, ""), ctx);
    return ctx;
}

function loadCatalog() {
    const ctx = vm.createContext({ qsTr: (s) => s });
    vm.runInContext(catalogSource.replace(/^\.pragma library\s*$/m, ""), ctx);
    return ctx;
}

test("dashboard and category charging indicator agree with toggle logic on Starting/Calibrating", () => {
    // Bug: CommandCatalog.nearlyCharging() treats Starting/Calibrating as
    // charging (category shows "Stop Charging"), but FirstPage battery label
    // and CategoryPage subtitle only check === "Charging", so during those
    // states the dashboard says "80%" with no charging hint while the
    // category offers Stop. Desired: all three agree.
    const catalog = loadCatalog();
    for (const state of ["Starting", "Calibrating"]) {
        assert.equal(
            vm.runInContext(`nearlyCharging({chargingState: "${state}"})`, catalog),
            true,
            `catalog must treat ${state} as charging`,
        );
    }
    for (const source of [firstPageSource, categorySource]) {
        assert.ok(
            source.includes("Starting") && source.includes("Calibrating"),
            "charging indicator only checks === \"Charging\"; Starting/Calibrating show stale non-charging UI while toggles treat them as charging",
        );
    }
});

test("NavigationPage replacement share clears an in-flight send", () => {
    // Bug: setSharedText() clears pendingPreviewId/previewing only, leaving
    // pendingSendId/sending/resultText. A second share arriving while a send
    // is in flight lets the old text's reply render as the new share's
    // result. The VIN-change path clears both; share-reuse must too.
    const page = {
        navSeq: 0, previewing: false, sending: true,
        pendingPreviewId: "", pendingSendId: "nav:send#1@123:0.1:VIN-A",
        resultText: "old result", previewText: "old preview",
    };
    const context = vm.createContext({
        page,
        destField: { text: "old" },
        qsTr: (s) => s,
        Date: { now: () => 999 },
        Math: { random: () => 0.5 },
        teslaClient: { previewDestination: () => {}, shareDestination: () => {} },
    });
    loadFunctions(navigationSource, ["preview", "setSharedText", "send"], context);
    context.setSharedText("New destination");
    assert.equal(page.pendingSendId, "", `old send ${JSON.stringify(page.pendingSendId)} survives replacement share and can render as the new result`);
    assert.equal(page.sending, false, "in-flight sending flag survives replacement share and blocks the new send");
});

test("NavigationPage successful share with empty output still confirms", () => {
    // Bug: onShareFinished does `ok ? output : ...` with no || "OK" fallback
    // (CategoryPage has `stdOut.length ? stdOut : "OK"`). An empty-output
    // success sets resultText="" which hides the label (visible: length>0),
    // so the user gets no confirmation. Desired: non-empty confirmation.
    const page = { pendingSendId: "id-1", sending: true, resultText: "" };
    const context = vm.createContext({
        page, requestId: "id-1", ok: true, output: "", errorMessage: "",
        qsTr: (s) => s,
    });
    runHandler(navigationSource, "onShareFinished", context);
    assert.equal(page.sending, false);
    assert.ok(
        page.resultText.length > 0,
        "empty-output success left resultText empty; confirmation label stays hidden",
    );
});

test("PairingPage VIN change invalidates a pending list-keys reply", () => {
    // Bug: every other page clears its pending IDs on VIN change (FirstPage,
    // CategoryPage, NavigationPage, CoverPage), but PairingPage.onConfigLoaded
    // never touches pendingListKeysId. Switching VIN while list-keys is in
    // flight lets the old VIN's reply render as the new VIN's keys.
    // Desired: pendingListKeysId cleared on VIN change.
    const page = {
        configReady: true, vin: "VIN-A", hasVin: true,
        publicKeyPem: "", pendingListKeysId: "list-keys#1:VIN-A",
        keysListOutput: "old",
    };
    const context = vm.createContext({
        page, vin: "VIN-B", model: "", hasKey: true, publicKeyPem: "",
        qsTr: (s) => s,
    });
    runHandler(pairingSource, "onConfigLoaded", context);
    assert.equal(page.vin, "VIN-B");
    assert.equal(page.pendingListKeysId, "", `stale list-keys ${JSON.stringify(page.pendingListKeysId)} survives VIN switch and can render old VIN keys`);
});

test("PairingPage List Enrolled Keys is single-flight", () => {
    // Bug: the List button has no enabled guard or busy indicator, so rapid
    // taps overwrite pendingListKeysId and the first reply is silently
    // dropped (or two BLE commands stack). Every other command surface gates
    // on pending/busy. Desired: button disabled while a list is pending.
    const listButton = pairingSource.match(/text: qsTr\("List Enrolled Keys"\)([\s\S]*?)\n                onClicked/);
    assert.ok(listButton, "List Enrolled Keys button must be discoverable");
    assert.match(
        listButton[1],
        /enabled/,
        "List Enrolled Keys button has no enabled/busy guard; double-tap stacks BLE commands and drops the first reply",
    );
});

test("every command surface handles a failed config load", () => {
    // Bug: only FirstPage and SettingsPage handle onConfigLoadError (queue-full
    // get_config emits configLoadError, not configLoaded). CategoryPage,
    // NavigationPage, PairingPage and CoverPage have no handler, so their
    // configReady stays false forever: delegates disabled, Pairing spinner
    // spins, no message, no retry. Desired: each surface reports the failure.
    for (const [name, source] of [
        ["CategoryPage", categorySource],
        ["NavigationPage", navigationSource],
        ["PairingPage", pairingSource],
        ["CoverPage", coverSource],
    ]) {
        assert.match(
            source,
            /onConfigLoadError/,
            `${name} has no onConfigLoadError; a queue-full get_config leaves it disabled/spinning with no message`,
        );
    }
});

test("ArgumentDialog rejects whitespace-only and non-numeric required input", () => {
    // Bug: revalidate() checks `length === 0` without trim() and performs no
    // numeric check for int/float text fields (schedule-remove ID,
    // precondition ID, DAYS/TIME). "   " or "abc" validates and only fails
    // after a BLE round-trip. Desired: trim + numeric guard like the backend.
    const revalidate = dialogSource.match(/function revalidate\(\) \{([\s\S]*?)\n    \}/);
    assert.ok(revalidate, "revalidate must be discoverable");
    for (const [value, kind] of [["   ", "whitespace"], ["abc", "non-numeric"]]) {
        const context = vm.createContext({
            commandDef: { args: [{ name: "ID", type: "int", __value: value }] },
            dialog: { formValid: true },
            formValid: true,
        });
        vm.runInContext(`${revalidate[0]}\nrevalidate();\nthis.__result = formValid;`, context);
        assert.equal(
            vm.runInContext("__result", context),
            false,
            `required int field ${JSON.stringify(value)} (${kind}) validated; backend rejects it only after BLE`,
        );
    }
});

test("VehicleState.guessModel ignores surrounding whitespace", () => {
    // Bug: guessModel() uppercases but never trims, so a pasted " 5YJ3... "
    // (Settings VIN field, clipboard paste) misses every prefix and returns
    // "" instead of "model3". The car image falls back to default and the
    // model picker preselect is lost. Desired: trim before matching.
    const ctx = loadVehicleState();
    assert.equal(ctx.guessModel("5YJ3E1EA0PF000000"), "model3");
    assert.equal(
        ctx.guessModel("  5YJ3E1EA0PF000000  "),
        "model3",
        'pasted VIN with surrounding spaces mis-guesses to "" instead of "model3"',
    );
});

test("Rust preview ABI clears output slots on parse failure", () => {
    // Bug: core_preview_destination's Err arm sets only ok=false +
    // error_message, leaving kind/value1/value2 untouched (same stale-slot
    // class as the already-fixed core_run/core_pair). A caller reusing slots
    // reads the previous preview's kind/values after a failure.
    // core_run's hard-error path clears to ""/-1; preview must do the same.
    const ffi = fs.readFileSync(path.join(__dirname, "../../helper/src/ffi.rs"), "utf8");
    const errArm = ffi.match(/pub unsafe extern "C" fn core_preview_destination[\s\S]*?Err\(e\) => \{([\s\S]*?)\n        \}/);
    assert.ok(errArm, "core_preview_destination Err arm must be discoverable");
    assert.match(errArm[1], /kind/, "preview failure leaves kind slot untouched; stale kind leaks into next preview");
    assert.match(errArm[1], /value1/, "preview failure leaves value1 slot untouched");
});

test("Rust share ABI clears stdout on transport failure", () => {
    // Same stale-slot shape for core_share_destination's Err arm:
    // core_pair's Err arm clears stdout_out to "", but share leaves it
    // untouched so a caller reusing slots reads the previous share's output.
    const ffi = fs.readFileSync(path.join(__dirname, "../../helper/src/ffi.rs"), "utf8");
    const errArm = ffi.match(/pub unsafe extern "C" fn core_share_destination[\s\S]*?Err\(e\) => \{([\s\S]*?)\n        \}/);
    assert.ok(errArm, "core_share_destination Err arm must be discoverable");
    assert.match(errArm[1], /stdout_out/, "share failure leaves stdout_out untouched; stale output leaks into next share");
});
