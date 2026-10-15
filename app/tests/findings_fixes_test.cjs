const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { loadFunctions, runHandler, runRootHandler } = require("./qml_helpers.cjs");
const source = (file) => fs.readFileSync(path.join(__dirname, "../qml", file), "utf8");

test("category VIN changes invalidate pending replies", () => {
    // vehicleStatus is the dashboard's live reading, which the dashboard
    // itself clears on a VIN change (production_vehicle_switch_test).
    const qml = source("pages/CategoryPage.qml");
    const page = { vin: "VIN-B", pendingCmd: "lock:VIN-A", pendingLabel: "Lock", lastResult: "old" };
    const context = vm.createContext({ page, requestId: page.pendingCmd, ok: true, stdOut: "stale", stdErr: "", qsTr: (s) => s });
    runRootHandler(qml, "onVinChanged", context);
    runHandler(qml, "onCommandFinished", context);
    assert.equal(page.pendingCmd, "");
    assert.equal(page.lastResult, "");
});

test("navigation VIN changes invalidate preview and send replies", () => {
    const qml = source("pages/NavigationPage.qml");
    const page = { vin: "VIN-B", pendingPreviewId: "preview:VIN-A", pendingSendId: "send:VIN-A", previewing: true, sending: true, previewText: "old", resultText: "old" };
    const context = vm.createContext({ page, requestId: page.pendingPreviewId });
    runRootHandler(qml, "onVinChanged", context);
    runHandler(qml, "onDestinationPreviewed", context);
    context.requestId = "send:VIN-A";
    runHandler(qml, "onShareFinished", context);
    assert.equal(page.pendingPreviewId, "");
    assert.equal(page.pendingSendId, "");
    assert.equal(page.previewing, false);
    assert.equal(page.sending, false);
    assert.equal(page.previewText, "");
    assert.equal(page.resultText, "");
});

test("a duplicate cover reply cannot clear the next command's busy state", () => {
    const qml = source("cover/CoverPage.qml");
    const cover = { vin: "VIN-A", canCommand: true, commandBusy: false, requestSeq: 0 };
    const context = vm.createContext({ cover, ok: true, teslaClient: { runCommand: () => {} } });
    loadFunctions(qml, ["runCoverCommand"], context);
    context.runCoverCommand("lock");
    const first = cover.pendingCmd;
    context.requestId = first;
    runHandler(qml, "onCommandFinished", context);
    context.runCoverCommand("lock");
    const second = cover.pendingCmd;
    runHandler(qml, "onCommandFinished", context);
    runHandler(qml, "onCommandError", context);
    assert.equal(cover.commandBusy, true);
    assert.equal(cover.pendingCmd, second);
});

test("settings preserves blurred dirty fields and an unknown model sentinel", () => {
    const qml = source("pages/SettingsPage.qml");
    const page = { vinDirty: true, keyNameDirty: true, configReady: true };
    const vinField = { text: "edited VIN", activeFocus: false };
    const keyNameField = { text: "edited name", activeFocus: false };
    const modelField = { currentIndex: 0 };
    const context = vm.createContext({ page, vinField, keyNameField, modelField, connectTimeoutSlider: {}, commandTimeoutSlider: {}, coverLeftField: {}, coverRightField: {}, coverLeftAction: "trunk-open", coverRightAction: "frunk-open", CoverActions: { indexOf: (_, fallback) => fallback }, vin: "old VIN", model: "future-model", keyName: "old name", connectTimeoutSec: 20, commandTimeoutSec: 5, hasKey: true, VState: { modelIndex: () => -1 }, qsTr: (s) => s });
    runHandler(qml, "onConfigLoaded", context);
    assert.equal(vinField.text, "edited VIN");
    assert.equal(keyNameField.text, "edited name");
    assert.equal(modelField.currentIndex, -1);
    assert.equal(page.savedModel, "future-model");
});
