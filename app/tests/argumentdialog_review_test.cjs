const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

// Execute the real catalog and QML acceptance handler without Sailfish-only
// imports. No copy of the serialization logic is maintained in this test.
const qmlRoot = path.join(__dirname, "../qml");
const catalogSource = fs.readFileSync(path.join(qmlRoot, "js/CommandCatalog.js"), "utf8");
const dialogSource = fs.readFileSync(path.join(qmlRoot, "pages/ArgumentDialog.qml"), "utf8");
const acceptanceHandler = dialogSource.match(/onAccepted:\s*\{([\s\S]*?)\n    \}/);
assert.ok(acceptanceHandler, "ArgumentDialog acceptance handler must be discoverable");

function serialize(factory, input) {
    const context = vm.createContext({ qsTr: (s) => s });
    vm.runInContext(catalogSource.replace(/^\.pragma library\s*$/m, ""), context);
    const args = vm.runInContext(`${factory}()`, context);
    args.forEach((arg) => { arg.__value = input[arg.name] ?? ""; });
    context.commandDef = { args };
    vm.runInContext(acceptanceHandler[1], context);
    return Array.from(context.values);
}

test("charge schedule ENABLED retains the vendor's REPEAT/ID/ENABLED positions", () => {
    const values = serialize("chargeScheduleAddArgs", {
        DAYS: "all", TIME: "22:00-06:00", LATITUDE: "48.8584", LONGITUDE: "2.2945",
        REPEAT: "once", ENABLED: "false",
    });
    // The Go handler consumes ID at index 5, even though the UI hides it.
    assert.equal(values[6], "false", "disabled must reach ENABLED, not the ignored ID slot");
});

test("precondition schedule ID is not shifted into an omitted REPEAT slot", () => {
    const values = serialize("preconditionScheduleAddArgs", {
        DAYS: "all", TIME: "06:00", LATITUDE: "48.8584", LONGITUDE: "2.2945",
        ID: "12345", ENABLED: "false",
    });
    assert.equal(values[5], "12345", "editing a schedule must send its ID in the ID slot");
    assert.equal(values[6], "false", "disabled must reach ENABLED");
});

test("QML command arity and schedule slots match the actual Go command table", () => {
    const go = fs.readFileSync(path.join(__dirname, "../../helper/session/commands_vendor.go"), "utf8");
    const contracts = new Map();
    for (const match of go.matchAll(/^\t"([a-z0-9-]+)":\s*\{([\s\S]*?)^\t\},/gm)) {
        function slots(field) {
            const section = match[2].match(new RegExp(`^\\t\\t${field}:\\s*\\[\\]Argument\\{([\\s\\S]*?)^\\t\\t\\},`, "m"));
            return section ? Array.from(section[1].matchAll(/name:\s*"([^"]+)"/g), (m) => m[1]) : [];
        }
        contracts.set(match[1], { required: slots("args"), optional: slots("optional") });
    }
    assert.ok(contracts.size > 40, "Go command table extraction failed");
    const context = vm.createContext({ qsTr: (s) => s });
    vm.runInContext(catalogSource.replace(/^\.pragma library\s*$/m, ""), context);
    for (const category of context.CATEGORIES) {
        for (const command of category.commands) {
            const contract = contracts.get(command.id);
            assert.ok(contract, `missing backend command ${command.id}`);
            assert.equal(command.args.length, contract.required.length + contract.optional.length,
                `${command.id} must represent every backend slot, including hidden optionals`);
            if (command.id === "charging-schedule-add" || command.id === "precondition-schedule-add") {
                assert.deepEqual(Array.from(command.args, (a) => a.name),
                    contract.required.concat(contract.optional), `${command.id} positional contract drift`);
            }
        }
    }
});

test("omitted trailing optional arguments remain omitted", () => {
    assert.deepEqual(serialize("preconditionScheduleAddArgs", {
        DAYS: "all", TIME: "06:00", LATITUDE: "48.8584", LONGITUDE: "2.2945",
    }), ["all", "06:00", "48.8584", "2.2945"]);
});

test("Loader initializes unchanged defaults after component completion", () => {
    const loaded = dialogSource.match(/onLoaded:\s*\{([\s\S]*?)\n                    \}/);
    assert.ok(loaded, "Loader initialization handler must be discoverable");
    for (const [spec, expected] of [
        [{ type: "enum", values: ["left", "right"], __value: "stale" }, "left"],
        [{ type: "float", min: -90, max: 90, def: 0 }, "0"],
        [{ type: "float", min: 15, max: 28, def: 21, sendSuffix: "C" }, "21C"],
        [{ type: "enum", optional: true, values: ["once"], __value: "once" }, ""],
    ]) {
        let validations = 0;
        const item = {};
        const context = vm.createContext({ argSpec: spec, item, dialog: { revalidate() { validations++; } } });
        vm.runInContext(loaded[1], context);
        assert.equal(spec.__value, expected);
        assert.equal(item.argSpec, spec);
        assert.equal(validations, 1, "default fields must update form validity");
    }
});
