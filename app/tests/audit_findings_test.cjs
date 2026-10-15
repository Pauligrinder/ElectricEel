const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { loadFunctions, runHandler } = require("./qml_helpers.cjs");

// Production audit findings — regression tests that FAIL on the current
// code and PASS once the underlying logic bug is fixed. Each test executes
// the real QML/JS handler (no source-token shortcuts) except where the bug
// itself is a misplaced source statement (Settings auto-guess).
const qmlRoot = path.join(__dirname, "../qml");
const firstPageSource = fs.readFileSync(path.join(qmlRoot, "pages/FirstPage.qml"), "utf8");
const categorySource = fs.readFileSync(path.join(qmlRoot, "pages/CategoryPage.qml"), "utf8");
const pairingSource = fs.readFileSync(path.join(qmlRoot, "pages/PairingPage.qml"), "utf8");
const settingsSource = fs.readFileSync(path.join(qmlRoot, "pages/SettingsPage.qml"), "utf8");
const vehicleStateSource = fs.readFileSync(path.join(qmlRoot, "js/VehicleState.js"), "utf8");

function loadVehicleState(extra = {}) {
    const context = vm.createContext({ qsTr: (s) => s, console, Date, ...extra });
    vm.runInContext(vehicleStateSource.replace(/^\.pragma library\s*$/m, ""), context);
    return context;
}

test("AUDIT-1: a later successful status leg must not clear an earlier leg failure", () => {
    // Chain is body -> closures -> climate -> charge. If body fails (e.g.
    // infotainment asleep) but closures succeeds, the dashboard must still
    // show the body error instead of reporting a fully fresh reading.
    // Current FirstPage.qml does `if (ok) statusError = ""` on every leg,
    // wiping the earlier failure.
    const ctx = loadVehicleState();
    vm.runInContext("var VState = this;", ctx);
    ctx.page = {
        vin: "VIN", hasKey: true, vehicleStatus: ctx.emptyStatus(),
        statusBeforeToggle: null, statusStage: "body", statusRequestId: "status:body#1@test:VIN",
        statusError: "", statusSeq: 1, statusInstance: "test",
    };
    const requests = [];
    ctx.teslaClient = {
        helperAvailable: true,
        runCommand: (id, command, args) => requests.push({ id, command, args }),
    };
    ctx.qsTr = (s) => s;
    loadFunctions(firstPageSource, ["runStatus"], ctx);
    // Body fails.
    ctx.requestId = ctx.page.statusRequestId;
    ctx.ok = false;
    ctx.stdOut = "";
    ctx.stdErr = "body-controller-state timeout";
    ctx.exitCode = 1;
    runHandler(firstPageSource, "onCommandFinished", ctx);
    assert.equal(ctx.page.statusStage, "closures");
    assert.match(ctx.page.statusError, /body-controller-state timeout/);
    // Closures succeeds — must NOT wipe the body error.
    ctx.requestId = ctx.page.statusRequestId;
    ctx.ok = true;
    ctx.stdOut = '{"closuresState":{"locked":true}}';
    ctx.stdErr = "";
    runHandler(firstPageSource, "onCommandFinished", ctx);
    assert.match(
        ctx.page.statusError,
        /body-controller-state timeout/,
        "a later ok leg cleared an earlier leg failure; dashboard reports fresh while doors/climate may be stale",
    );
});

test("AUDIT-2: CategoryPage commands must request post-command status refresh", () => {
    // FirstPage toggles call runCommand(..., true) so Rust waits for
    // telemetry to settle and emits statusRefreshRequested. CategoryPage
    // offers the same lock/unlock/climate/charging commands but calls
    // runCommand without the refresh flag, leaving the dashboard on its
    // optimistic snapshot until a manual refresh.
    let captured = null;
    const context = vm.createContext({
        page: { requestSeq: 0, pendingCmd: "", pendingLabel: "", lastResult: "", vin: "VIN" },
        teslaClient: {
            runCommand: (id, cmd, args, refresh) => {
                captured = { id, cmd, args, refresh };
                context.page.pendingCmd = id;
            },
        },
    });
    loadFunctions(categorySource, ["execute"], context);
    context.execute({ id: "lock", label: "Lock" }, []);
    assert.ok(captured, "execute must submit the command");
    assert.equal(
        captured.refresh,
        true,
        `CategoryPage.execute(lock) passed refresh=${captured.refresh}; expected true like FirstPage.toggleLock`,
    );
});

test("AUDIT-3: empty body-controller-state payload must not refresh freshness", () => {
    // mergeClosuresState/mergeClimateState/mergeChargeState early-return
    // without bumping updatedAt when their wrapper is missing. Only
    // mergeBodyControllerState unconditionally markUpdated()s, so "{}" or an
    // unknown lock enum still makes the dashboard read "Updated just now"
    // with zero new data.
    const ctx = loadVehicleState();
    const base = ctx.emptyStatus();
    const out = ctx.mergeBodyControllerState(base, "{}");
    assert.equal(
        out.bodyUpdatedAt,
        0,
        "empty {} bumped bodyUpdatedAt; dashboard freshness must advance only on real telemetry",
    );
    assert.equal(
        out.updatedAt,
        0,
        "empty {} bumped aggregate updatedAt",
    );
    for (const payload of ['{"vehicleLockState":"UNKNOWN"}', '{"closureStatuses":{}}']) {
        const unchanged = ctx.mergeBodyControllerState(base, payload);
        assert.equal(unchanged.bodyUpdatedAt, 0, `${payload} contains no usable reading`);
        assert.equal(unchanged.locked, null);
    }
});

test("AUDIT-4: typing a VIN must not persist the auto-guess as an explicit model", () => {
    // SettingsPage.vinField.onTextChanged sets modelDirty=true while
    // auto-selecting guessModel(text). Save then stores e.g. "model3"
    // instead of ""/Auto, defeating Auto and freezing the guess against
    // later VIN changes. Only an explicit ComboBox pick should mark dirty.
    const vinBlock = settingsSource.match(/id:\s*vinField[\s\S]*?onTextChanged:\s*\{([\s\S]*?)\n                \}/);
    assert.ok(vinBlock, "vinField.onTextChanged must be discoverable");
    assert.doesNotMatch(
        vinBlock[1],
        /modelDirty\s*=\s*true/,
        "VIN typing marks modelDirty; auto-guess is persisted as an explicit override instead of Auto",
    );
    // Changing currentIndex programmatically emits the same signal as a
    // manual selection. Exercise it so removing only the assignment above
    // cannot accidentally leave Auto broken through the ComboBox handler.
    const modelBlock = settingsSource.match(/id:\s*modelField[\s\S]*?onCurrentIndexChanged:\s*\{([\s\S]*?)\n                \}/);
    assert.ok(modelBlock);
    const page = { loadingConfig: false, configReady: true, modelDirty: false };
    const context = vm.createContext({
        page, activeFocus: true, text: "5YJ3E1EA0PF000000",
        VState: { guessModel: () => "model3", modelIndex: () => 3 },
    });
    const indexChanged = vm.runInContext(`(function() { ${modelBlock[1]} })`, context);
    context.modelField = { set currentIndex(value) { indexChanged(); } };
    vm.runInContext(`(function() { ${vinBlock[1]} })()`, context);
    assert.equal(page.vinDirty, true);
    assert.equal(page.modelDirty, false, "auto-guess triggered dirty through currentIndexChanged");
    indexChanged();
    assert.equal(page.modelDirty, true, "explicit model selection must still be saved");
});

test("AUDIT-5: successful pairing must refresh config so the dashboard leaves No-key state", () => {
    // PairingPage.onPaired only sets labels. FirstPage.hasKey and the
    // public-key display stay stale until the page is re-entered. The
    // handler must call teslaClient.refreshConfig() on success.
    let refreshes = 0;
    const page = { pairing: true, pairStatus: "", pendingListKeysId: "" };
    const context = vm.createContext({
        page,
        qsTr: (s) => s,
        teslaClient: { refreshConfig: () => { refreshes++; } },
        ok: true, output: "paired", errorMessage: "",
    });
    runHandler(pairingSource, "onPaired", context);
    assert.equal(page.pairing, false);
    assert.equal(
        refreshes,
        1,
        "onPaired(ok=true) did not refreshConfig; front page still shows No key after a successful pair",
    );
});
