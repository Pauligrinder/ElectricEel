const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { loadFunctions, runHandler } = require("./qml_helpers.cjs");

// Production review findings: each test encodes the DESIRED behavior.
// All tests FAIL on the current code and PASS once the fix lands.
// See docs/production-review.md for the previous review cycle; this file
// is the new cycle's regression net.

const qmlRoot = path.join(__dirname, "../qml");
const firstPageSource = fs.readFileSync(path.join(qmlRoot, "pages/FirstPage.qml"), "utf8");
const categorySource = fs.readFileSync(path.join(qmlRoot, "pages/CategoryPage.qml"), "utf8");
const navigationSource = fs.readFileSync(path.join(qmlRoot, "pages/NavigationPage.qml"), "utf8");
const pairingSource = fs.readFileSync(path.join(qmlRoot, "pages/PairingPage.qml"), "utf8");
const coverSource = fs.readFileSync(path.join(qmlRoot, "cover/CoverPage.qml"), "utf8");
const settingsSource = fs.readFileSync(path.join(qmlRoot, "pages/SettingsPage.qml"), "utf8");

test("PairingPage clears a stale PEM when the config reports hasKey=false", () => {
    // Bug: onConfigLoaded only assigns when hasKey is true, so a previously
    // shown PEM stays visible and keeps "Pair with Vehicle" enabled after the
    // key was deleted. Desired: hasKey=false clears publicKeyPem.
    const page = {
        configReady: false, vin: "OLD", hasVin: true,
        publicKeyPem: "-----BEGIN PUBLIC KEY-----\nOLD\n-----END PUBLIC KEY-----",
    };
    const context = vm.createContext({
        page,
        vin: "5YJ3E1EA0PF000000", hasKey: false, publicKeyPem: "",
        qsTr: (s) => s,
    });
    runHandler(pairingSource, "onConfigLoaded", context);
    assert.equal(page.publicKeyPem, "", "stale PEM survived hasKey=false; Pair stays enabled with a non-existent key");
});

test("CoverPage uses unique correlation IDs per invocation", () => {
    // Bug: runCoverCommand("lock") always submits "cover:lock". FirstPage /
    // CategoryPage / NavigationPage all use seq+Date.now()+random. A delayed
    // or duplicate reply for run N clears busy for run N+1 (prefix match).
    // Desired: every submission has a distinct requestId.
    const ids = [];
    const cover = { canCommand: true, commandBusy: false };
    const context = vm.createContext({
        cover,
        teslaClient: { runCommand: (id) => ids.push(id) },
    });
    loadFunctions(coverSource, ["runCoverCommand"], context);
    context.runCoverCommand("lock");
    cover.commandBusy = false;
    context.runCoverCommand("lock");
    assert.equal(ids.length, 2);
    assert.notEqual(ids[0], ids[1], `cover reused correlation ID ${JSON.stringify(ids[0])}`);
});

test("CategoryPage scopes command correlation to the VIN", () => {
    // Bug: pendingCmd is `cmd#seq@time:random` with no VIN and no
    // onConfigLoaded invalidation (FirstPage embeds `:vin` + resets on VIN
    // change). Switching VIN in Settings while a command is in flight lets
    // the old VIN's reply render as the new VIN's result.
    // Desired: the pending ID embeds the VIN active at submit time.
    const page = { requestSeq: 0, pendingCmd: "", pendingLabel: "", lastResult: "", vin: "VIN-A" };
    const context = vm.createContext({
        page,
        Date: { now: () => 12345 },
        Math: { random: () => 0.42 },
        teslaClient: { runCommand: () => {} },
    });
    loadFunctions(categorySource, ["execute"], context);
    context.execute({ id: "lock", label: "Lock" }, []);
    assert.match(page.pendingCmd, /VIN-A/, `pendingCmd ${JSON.stringify(page.pendingCmd)} carries no VIN; stale-VIN replies cannot be rejected`);
});

test("NavigationPage scopes preview/send correlation to the VIN", () => {
    // Same VIN race as CategoryPage for previewDestination/shareDestination:
    // Rust executes against global config at execution time, so a preview
    // started on VIN A that completes after a switch to VIN B must be ignored.
    // Desired: pending IDs embed the VIN active at submit time.
    const page = {
        navSeq: 0, previewing: false, sending: false,
        pendingPreviewId: "", pendingSendId: "", resultText: "", previewText: "",
        vin: "VIN-A",
    };
    const context = vm.createContext({
        page,
        destField: { text: "Berlin" },
        Date: { now: () => 12345 },
        Math: { random: () => 0.42 },
        qsTr: (s) => s,
        teslaClient: {
            previewDestination: () => {},
            shareDestination: () => {},
        },
    });
    loadFunctions(navigationSource, ["preview", "send"], context);
    context.preview();
    context.send();
    assert.match(page.pendingPreviewId, /VIN-A/, `pendingPreviewId ${JSON.stringify(page.pendingPreviewId)} carries no VIN`);
    assert.match(page.pendingSendId, /VIN-A/, `pendingSendId ${JSON.stringify(page.pendingSendId)} carries no VIN`);
});

test("FirstPage does not let a first-leg error mask later fresh telemetry", () => {
    // Bug: body failure sets statusError; closures/climate/charge only set
    // `else if (statusError.length === 0)` and successes never clear. Chain
    // body-fail + three later successes still shows "Status unavailable"
    // despite fresh battery/climate readings (label prioritises statusError).
    // Desired: any successful leg clears a stale error (or per-leg errors).
    const requests = [];
    const context = vm.createContext({ qsTr: (s) => s, console });
    vm.runInContext(
        fs.readFileSync(path.join(qmlRoot, "js/VehicleState.js"), "utf8").replace(/^\.pragma library\s*$/m, ""),
        context,
    );
    context.VState = context;
    context.page = {
        vin: "VIN", hasKey: true, vehicleStatus: context.emptyStatus(),
        statusBeforeToggle: null, statusStage: "", statusRequestId: "", statusError: "",
        statusSeq: 0, statusInstance: "test",
    };
    context.teslaClient = {
        helperAvailable: true,
        runCommand: (id, command, args) => requests.push({ id, command, args }),
    };
    loadFunctions(firstPageSource, ["runStatus", "refreshStatus"], context);
    context.refreshStatus();
    // Body leg fails.
    context.requestId = context.page.statusRequestId;
    context.ok = false;
    context.stdOut = "";
    context.stdErr = "transient VCSEC timeout";
    context.exitCode = 1;
    runHandler(firstPageSource, "onCommandFinished", context);
    // Later legs succeed with fresh telemetry.
    for (const [payload] of [
        ['{"closuresState":{"locked":true}}'],
        ['{"climateState":{"insideTempCelsius":21}}'],
        ['{"chargeState":{"batteryLevel":80}}'],
    ]) {
        context.requestId = context.page.statusRequestId;
        context.ok = true;
        context.stdOut = payload;
        context.stdErr = "";
        runHandler(firstPageSource, "onCommandFinished", context);
    }
    assert.equal(context.page.vehicleStatus.batteryLevel, 80);
    assert.equal(context.page.statusError, "", `stale first-leg error ${JSON.stringify(context.page.statusError)} masks fresh telemetry`);
});

test("FirstPage hard toggle failure re-fetches instead of leaving a stale icon", () => {
    // Bug: submit() queue-full emits commandError locally without Rust ever
    // seeing the op, so Rust's delayed statusRefreshRequested never arrives.
    // onCommandError restores statusBeforeToggle but never calls
    // refreshStatus(), despite its own comment claiming it "re-fetches the
    // same way onCommandFinished does". Desired: a new status request.
    const requests = [];
    const context = vm.createContext({ qsTr: (s) => s, console });
    vm.runInContext(
        fs.readFileSync(path.join(qmlRoot, "js/VehicleState.js"), "utf8").replace(/^\.pragma library\s*$/m, ""),
        context,
    );
    context.VState = context;
    context.page = {
        vin: "VIN", hasKey: true, vehicleStatus: { ...context.emptyStatus(), locked: true },
        statusBeforeToggle: null, statusStage: "", statusRequestId: "", statusError: "",
        statusSeq: 0, statusInstance: "test",
    };
    context.teslaClient = {
        helperAvailable: true,
        runCommand: (id, command, args) => requests.push({ id, command, args }),
    };
    loadFunctions(firstPageSource, ["runStatus", "setOptimistic", "toggleLock", "refreshStatus"], context);
    context.toggleLock();
    const afterToggle = requests.length;
    assert.equal(afterToggle, 1);
    context.requestId = context.page.statusRequestId;
    context.message = "Control runtime unavailable or request queue full";
    runHandler(firstPageSource, "onCommandError", context);
    assert.ok(
        requests.length > afterToggle,
        `toggle hard-failure issued no re-fetch (requests stayed at ${afterToggle}); dashboard keeps the never-requested optimistic icon`,
    );
});

test("SettingsPage does not clobber a dirty user edit on a late config reply", () => {
    // Bug: onConfigLoaded unconditionally assigns vinField.text etc. Open
    // Settings, type a new VIN, delayed first reply lands -> edit lost.
    // loadingConfig only suppresses model auto-guess, not the overwrite.
    // Desired: a focused/dirty field keeps the user's edit.
    const page = { loadingConfig: false, configReady: true, configLoading: false, statusText: "" };
    const vinField = { text: "user-typed-VIN", activeFocus: true };
    const context = vm.createContext({
        page,
        vinField,
        modelField: { currentIndex: 0 },
        keyNameField: { text: "user-typed-name", activeFocus: true },
        connectTimeoutSlider: { value: 20 },
        commandTimeoutSlider: { value: 5 },
        vin: "OLD-VIN-FROM-DELAYED-REPLY",
        model: "",
        keyName: "old-name",
        connectTimeoutSec: 20,
        commandTimeoutSec: 5,
        hasKey: false,
        publicKeyPem: "",
        qsTr: (s) => s,
        VState: { modelIndex: () => 0 },
    });
    runHandler(settingsSource, "onConfigLoaded", context);
    assert.equal(vinField.text, "user-typed-VIN", `late reply overwrote dirty VIN edit with ${JSON.stringify(vinField.text)}`);
});

test("VehicleState.modelIndex does not silently normalise unknown models to Auto", () => {
    // Bug: modelIndex(unknown) returns 0 (Auto). Settings assigns that to
    // currentIndex, so the next Save persists "" and destroys the saved
    // value (same clamp-then-persist shape as the timeout sliders once had).
    // Desired: unknown ids are distinguishable from Auto (e.g. -1) so the
    // caller can preserve/warn instead of overwriting.
    const state = vm.createContext({ qsTr: (s) => s });
    vm.runInContext(
        fs.readFileSync(path.join(qmlRoot, "js/VehicleState.js"), "utf8").replace(/^\.pragma library\s*$/m, ""),
        state,
    );
    assert.notEqual(state.modelIndex("future-model"), 0, 'modelIndex("future-model") === 0 (Auto); saving would erase the stored value');
});
