const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { randomUUID } = require("node:crypto");

const qmlRoot = path.join(__dirname, "../qml");

// Loads a real .pragma library from app/qml/js into its own context, with
// the QML string extension String.prototype.arg available to its code.
function loadLibrary(file) {
    const context = vm.createContext({ qsTr: (s) => s, console, Date, Math });
    vm.runInContext(`String.prototype.arg = function (value) {
        var match = this.match(/%(\\d+)/g);
        if (!match) return String(this);
        var lowest = match.map(function (m) { return Number(m.slice(1)); }).sort(function (a, b) { return a - b; })[0];
        return this.split("%" + lowest).join(String(value));
    };`, context);
    const source = fs.readFileSync(path.join(qmlRoot, "js", file), "utf8");
    vm.runInContext(source.replace(/^\.pragma library\s*$/m, ""), context);
    return context;
}

// QML pages import these libraries by alias. Provide the real ones when a
// test did not substitute its own.
function provideLibraries(context) {
    for (const [alias, file] of [
        ["Catalog", "CommandCatalog.js"],
        ["CommandText", "CommandText.js"],
        ["CoverActions", "CoverActions.js"],
    ]) {
        if (context[alias] === undefined)
            context[alias] = loadLibrary(file);
    }
}

function mockRequestIds(context) {
    if (context.teslaClient && !context.teslaClient.newRequestId)
        context.teslaClient.newRequestId = (prefix, vin = "") => `${prefix}#${randomUUID()}:${vin}`;
}

function loadFunctions(source, names, context) {
    mockRequestIds(context);
    provideLibraries(context);
    for (const name of names) {
        const match = source.match(new RegExp(`function ${name}\\([^)]*\\)\\s*\\{[\\s\\S]*?\\n    \\}`));
        assert.ok(match, `${name} must be discoverable`);
        vm.runInContext(match[0], context);
        if (context.page) context.page[name] = context[name];
    }
}

// Signal handlers inside a Connections block (8-space indentation).
function runHandler(source, name, context) {
    provideLibraries(context);
    const match = source.match(new RegExp(`${name}:\\s*\\{([\\s\\S]*?)\\n        \\}`));
    assert.ok(match, `${name} must be discoverable`);
    vm.runInContext(`(function() { ${match[1]} })()`, context);
}

// Property-change handlers declared on the page/cover root (4-space indentation).
function runRootHandler(source, name, context) {
    provideLibraries(context);
    const match = source.match(new RegExp(`\\n    ${name}:\\s*\\{([\\s\\S]*?)\\n    \\}`));
    assert.ok(match, `${name} must be discoverable`);
    vm.runInContext(`(function() { ${match[1]} })()`, context);
}

module.exports = { loadFunctions, runHandler, runRootHandler, loadLibrary };
