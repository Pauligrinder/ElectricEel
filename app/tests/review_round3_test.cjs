const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { loadFunctions, runHandler } = require("./qml_helpers.cjs");

// Review round 3: each test encodes the DESIRED behavior. They FAIL on the
// current code and PASS once the corresponding fix lands. They execute the
// real QML/JS sources (no copies of the buggy logic).

const qmlRoot = path.join(__dirname, "../qml");
const read = (rel) => fs.readFileSync(path.join(qmlRoot, rel), "utf8");
const firstPageSource = read("pages/FirstPage.qml");
const categorySource = read("pages/CategoryPage.qml");
const settingsSource = read("pages/SettingsPage.qml");
const dialogSource = read("pages/ArgumentDialog.qml");
const coverSource = read("cover/CoverPage.qml");
const vehicleStateSource = read("js/VehicleState.js");
const catalogSource = read("js/CommandCatalog.js");
const coverActionsSource = read("js/CoverActions.js");

function library(source) {
    const ctx = vm.createContext({ qsTr: (s) => s, console, Date, Math });
    vm.runInContext(source.replace(/^\.pragma library\s*$/m, ""), ctx);
    return ctx;
}

function catalogArg(commandId, argName) {
    const catalog = library(catalogSource);
    for (const category of catalog.CATEGORIES)
        for (const command of category.commands)
            if (command.id === commandId)
                return { command, arg: command.args.find((a) => a.name === argName) };
    throw new Error(`${commandId} not in catalog`);
}

function dashboard() {
    const requests = [];
    const context = vm.createContext({ qsTr: (s) => s, console });
    vm.runInContext(vehicleStateSource.replace(/^\.pragma library\s*$/m, ""), context);
    context.VState = context;
    context.page = {
        vin: "VIN A", model: "", hasKey: true, vehicleStatus: context.emptyStatus(),
        statusBeforeToggle: null, statusStage: "", statusRequestId: "", statusError: "",
    };
    context.teslaClient = {
        helperAvailable: true,
        runCommand: (id, command, args) => requests.push({ id, command, args }),
    };
    loadFunctions(firstPageSource, ["runStatus", "refreshStatus"], context);
    return { context, requests };
}

function finishChain(context) {
    for (const [stage, payload] of [
        ["body", '{"vehicleLockState":"VEHICLELOCKSTATE_LOCKED"}'],
        ["closures", '{"closuresState":{"locked":true}}'],
        ["climate", '{"climateState":{"insideTempCelsius":21}}'],
        ["charge", '{"chargeState":{"batteryLevel":80}}'],
    ]) {
        assert.equal(context.page.statusStage, stage);
        context.requestId = context.page.statusRequestId;
        context.ok = true;
        context.stdOut = payload;
        context.stdErr = "";
        context.exitCode = 0;
        runHandler(firstPageSource, "onCommandFinished", context);
    }
}

test("R3-1: cover lock/unlock requests the post-command dashboard refresh", () => {
    // Bug: CoverPage.runCoverCommand calls runCommand(id, cmd, []) without
    // refreshStatus, so Rust never emits status_refresh_requested after a
    // cover Lock/Unlock/Trunk. The dashboard keeps the old lock reading and
    // its padlock toggle then sends the wrong command (e.g. car locked from
    // the cover, dashboard still "unlocked" -> tap sends lock again instead
    // of unlock). Every dashboard/category command already passes true.
    // Desired: cover commands pass refreshStatus=true.
    const requests = [];
    const context = vm.createContext({
        cover: { vin: "VIN", canCommand: true, commandBusy: false, pendingCmd: "" },
        teslaClient: { runCommand: (...args) => requests.push(args) },
    });
    loadFunctions(coverSource, ["runCoverCommand"], context);
    context.runCoverCommand("lock");
    assert.equal(requests.length, 1);
    assert.equal(requests[0][1], "lock");
    assert.equal(requests[0][3], true, "cover command does not request a status refresh; dashboard lock state goes stale");
});

test("R3-2: a refresh requested while a status chain is running is not lost", () => {
    // Bug: refreshStatus() returns early while statusStage is non-empty, so a
    // status_refresh_requested that lands mid-chain (Rust emits it 2.5 s
    // after any refresh_status command, e.g. a CategoryPage Unlock queued
    // behind the chain that opening the category itself started) is dropped.
    // The running chain's body/closures legs were read BEFORE the command,
    // so the dashboard ends showing the pre-command state with "Updated just
    // now". Desired: remember the request and re-run once the chain ends.
    const { context, requests } = dashboard();
    context.refreshStatus();
    const onRefresh = firstPageSource.match(/onStatusRefreshRequested:\s*([^\n]+)/);
    assert.ok(onRefresh, "onStatusRefreshRequested must be discoverable");
    // Body leg in flight (read before the command) when the request lands.
    vm.runInContext(onRefresh[1], context);
    finishChain(context);
    assert.equal(requests.length, 5, "mid-chain refresh request was dropped; dashboard keeps pre-command telemetry");
    assert.equal(requests[4].command, "body-controller-state");
});

test("R3-3: an unchanged config reply does not start a BLE status chain", () => {
    // Bug: FirstPage.onConfigLoaded called refreshStatus() unconditionally.
    // configLoaded is broadcast, and CategoryPage, SettingsPage, PairingPage,
    // NavigationPage and the cover all called refreshConfig() on creation, so
    // merely opening a category queued 4 serial BLE round-trips (body,
    // closures, climate, charge) in front of the command the user is about
    // to tap. With the car out of range each leg waits connect+command
    // timeout, delaying e.g. Honk by minutes. Desired: a config reply for
    // the same VIN/key with telemetry already present issues no command.
    //
    // The dashboard now binds to TeslaClient's vin/hasKey properties, whose
    // change signals fire only for a different value (configload_test.cpp
    // checks that an identical config_loaded emits none). Any remaining
    // configLoaded handler must not refresh for unchanged values, and page
    // activation must not re-read the vehicle either.
    const { context, requests } = dashboard();
    context.page.vehicleStatus = context.mergeBodyControllerState(
        context.page.vehicleStatus, '{"vehicleLockState":"VEHICLELOCKSTATE_LOCKED"}');
    Object.assign(context, { vin: "VIN A", model: "", hasKey: true });
    if (/onConfigLoaded:/.test(firstPageSource))
        runHandler(firstPageSource, "onConfigLoaded", context);
    assert.equal(requests.length, 0, `unchanged config started ${requests.map((r) => r.command).join(", ")}`);
    assert.doesNotMatch(firstPageSource, /PageStatus\.Active[\s\S]{0,120}refresh/,
        "returning to the dashboard must not trigger a config/status re-read");
    const triggers = Array.from(firstPageSource.matchAll(/\n    (on[A-Z]\w*Changed):\s*\{[\s\S]*?\n    \}/g), (m) => m);
    for (const trigger of triggers) {
        if (/refreshStatus\(\)/.test(trigger[0]))
            assert.ok(["onVinChanged", "onHasKeyChanged"].includes(trigger[1]),
                `${trigger[1]} starts a status chain without a VIN/key change`);
    }
    const cpp = fs.readFileSync(path.join(__dirname, "configload_test.cpp"), "utf8");
    assert.match(cpp, /identical configuration/, "configload_test.cpp must cover change-only notifications");
});

test("R3-4: saving after typing a VIN keeps Auto instead of persisting the guess", () => {
    // Bug: AUDIT-4 stopped vinField from marking modelDirty, but Save still
    // sends VState.MODELS[modelField.currentIndex].id. Typing a VIN moves the
    // ComboBox to the guessed model, so Save persists e.g. "model3" as an
    // explicit override although the user never picked a model; a later VIN
    // change keeps the wrong silhouette. Desired: without an explicit pick
    // (modelDirty false) Save sends the loaded model ("" = Auto).
    const VState = library(vehicleStateSource);
    const saved = [];
    const page = { loadingConfig: false, configReady: true, modelDirty: false, savedModel: "", guessingModel: false };
    const context = vm.createContext({
        page, VState, CoverActions: library(coverActionsSource),
        activeFocus: true, text: "5YJ3E1EA0PF000000",
        vinField: { text: "5YJ3E1EA0PF000000" },
        modelField: { currentIndex: VState.modelIndex("") },
        keyNameField: { text: "phone" },
        connectTimeoutSlider: { value: 20 }, commandTimeoutSlider: { value: 5 },
        coverLeftField: { currentIndex: 0 }, coverRightField: { currentIndex: 1 },
        teslaClient: { setConfig: (...args) => saved.push(args) },
    });
    const vinBlock = settingsSource.match(/id:\s*vinField[\s\S]*?onTextChanged:\s*\{([\s\S]*?)\n                \}/);
    assert.ok(vinBlock, "vinField.onTextChanged must be discoverable");
    vm.runInContext(`(function() { ${vinBlock[1]} })()`, context);
    assert.equal(page.modelDirty, false);
    const save = settingsSource.match(/onClicked: (teslaClient\.setConfig\([\s\S]*?\))\n\s*\}/);
    assert.ok(save, "Save handler must be discoverable");
    vm.runInContext(save[1], context);
    assert.equal(saved.length, 1);
    assert.equal(saved[0][1], "", `auto-guessed ${JSON.stringify(saved[0][1])} persisted as an explicit model`);
});

test("R3-5: schedule coordinates are typed, not picked with a 180-million-step slider", () => {
    // Bug: ArgumentDialog picks sliderField for every required numeric arg
    // with min/max. LATITUDE/LONGITUDE (step 0.000001, range +-90/+-180)
    // become sliders: on a ~600 px track one pixel is ~0.3 degrees (~30 km),
    // so a real home location cannot be entered and the schedule is created
    // at 0,0 or a random point. Desired: coordinate args use a text field.
    const source = dialogSource.match(/sourceComponent:\s*\{([\s\S]*?)\n                    \}/);
    assert.ok(source, "Loader sourceComponent must be discoverable");
    for (const name of ["LATITUDE", "LONGITUDE"]) {
        const { arg } = catalogArg("charging-schedule-add", name);
        const context = vm.createContext({
            modelData: arg, enumField: "enum", pinField: "pin", sliderField: "slider", textField: "text",
        });
        const chosen = vm.runInContext(`(function() { ${source[1]} })()`, context);
        assert.equal(chosen, "text", `${name} rendered as ${chosen}; precise coordinates cannot be entered`);
    }
});

test("R3-6: negative schedule IDs are rejected before submission", () => {
    // Bug: schedule ID args are type "int" without min, and revalidate's int
    // pattern accepts a sign, so TYPE=id / ID=-5 enables Run. Rust's
    // protocol_commands::unsigned() rejects it only after the request is
    // queued behind BLE work. Desired: the dialog rejects negative IDs
    // (e.g. min: 0 on the catalog ID args).
    const revalidate = dialogSource.match(/function revalidate\(\) \{([\s\S]*?)\n    \}/);
    assert.ok(revalidate, "revalidate must be discoverable");
    for (const commandId of ["charging-schedule-remove", "precondition-schedule-remove"]) {
        const { command } = catalogArg(commandId, "ID");
        const commandDef = {
            args: command.args.map((a) => Object.assign({}, a, { __value: a.name === "TYPE" ? "id" : "-5" })),
        };
        const context = vm.createContext({ commandDef, formValid: true });
        vm.runInContext(`${revalidate[0]}\nrevalidate();\nthis.__result = formValid;`, context);
        assert.equal(vm.runInContext("__result", context), false, `${commandId} accepts ID -5; backend rejects it after queueing`);
    }
});

test("R3-7: opening a dialog never writes form state into the shared catalog", () => {
    // Bug: commandWithStateDefaults() returns the catalog's own commandDef
    // when vehicleStatus is null (CategoryPage nulls it on a VIN change).
    // ArgumentDialog then stores __value on CommandCatalog's .pragma library
    // singletons, leaking one dialog's input into the next dialog's initial
    // revalidate() (Run briefly enabled with stale values) and into every
    // CategoryPage. Desired: always hand the dialog a private copy.
    const catalog = library(catalogSource);
    const commandDef = catalog.findCategory("charging").commands.find((c) => c.id === "charging-set-limit");
    const context = vm.createContext({ page: { vehicleStatus: null } });
    loadFunctions(categorySource, ["commandWithStateDefaults"], context);
    const forDialog = context.commandWithStateDefaults(commandDef);
    assert.notEqual(forDialog.args[0], commandDef.args[0], "dialog receives the shared catalog arg object and will mutate it");
});
