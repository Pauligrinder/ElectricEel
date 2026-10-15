const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { loadFunctions, runHandler, runRootHandler } = require("./qml_helpers.cjs");

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
    // page.vin binds to TeslaClient.vin; a new value runs onVinChanged.
    const page = {
        configReady: true, vin: "VIN-B", hasVin: true,
        publicKeyPem: "", pendingListKeysId: "list-keys#1:VIN-A",
        keysListOutput: "old",
    };
    const context = vm.createContext({ page, qsTr: (s) => s });
    runRootHandler(pairingSource, "onVinChanged", context);
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
    // Bug: only FirstPage and SettingsPage handled a failed get_config
    // (queue full), so other surfaces stayed disabled/spinning with no
    // message or retry. TeslaClient now publishes configError; every
    // command surface must render it (pages via the shared banner).
    const firstPageSource = fs.readFileSync(path.join(qmlRoot, "pages/FirstPage.qml"), "utf8");
    for (const [name, source] of [
        ["FirstPage", firstPageSource],
        ["CategoryPage", categorySource],
        ["NavigationPage", navigationSource],
        ["PairingPage", pairingSource],
    ]) {
        assert.match(
            source,
            /ConfigErrorBanner\s*\{\s*client:\s*teslaClient\s*\}/,
            `${name} does not show the configuration error; a queue-full get_config leaves it disabled with no message`,
        );
    }
    assert.match(coverSource, /teslaClient\.configError/, "CoverPage does not show the configuration error");
    const banner = fs.readFileSync(path.join(qmlRoot, "pages/ConfigErrorBanner.qml"), "utf8");
    assert.match(banner, /client\.configError/);
    assert.match(banner, /refreshConfig\(\)/, "the banner must offer a retry");
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

test("Rust preview notification clears every value on parse failure", () => {
    // The UI receives previews only as runtime notifications (runtime_submit
    // -> dispatch -> DestinationPreviewed); the synchronous core_* slot ABI is
    // gone. A failed preview must still publish empty kind/value1/value2, never
    // values left over from an earlier preview. Behaviour is executed by the
    // Rust test runtime::tests::preview_and_share_failures_publish_empty_results.
    const runtime = fs.readFileSync(path.join(__dirname, "../../helper/src/runtime.rs"), "utf8");
    const arm = runtime.match(/Request::PreviewDestination \{ request_id, text \} => \{[\s\S]*?Err\(e\) => \(([\s\S]*?)\n\s*\),\n/);
    assert.ok(arm, "PreviewDestination failure arm must be discoverable");
    const fields = arm[1].split(",").map((field) => field.trim()).filter(Boolean);
    assert.deepEqual(
        fields.slice(0, 4),
        // kind is an Option<DestinationKind>; None serializes as "".
        ["false", "None", "String::new()", "String::new()"],
        "preview failure must publish ok=false with empty kind/value1/value2",
    );
});

test("Rust share notification clears output on failure", () => {
    // ShareFinished is the runtime-path equivalent of core_share_destination's
    // stdout slot: a failed share must publish an empty output.
    const runtime = fs.readFileSync(path.join(__dirname, "../../helper/src/runtime.rs"), "utf8");
    const arm = runtime.match(/Request::ShareDestination \{ request_id, text \} => \{[\s\S]*?unwrap_or_else\(\|e\| \((.*)\)\);/);
    assert.ok(arm, "ShareDestination failure arm must be discoverable");
    assert.match(arm[1], /^false, String::new\(\), e\.to_string\(\)$/, "share failure must publish an empty output");
});
