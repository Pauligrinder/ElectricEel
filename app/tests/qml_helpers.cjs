const assert = require("node:assert/strict");
const vm = require("node:vm");
const { randomUUID } = require("node:crypto");

function mockRequestIds(context) {
    if (context.teslaClient && !context.teslaClient.newRequestId)
        context.teslaClient.newRequestId = (prefix, vin = "") => `${prefix}#${randomUUID()}:${vin}`;
}

function loadFunctions(source, names, context) {
    mockRequestIds(context);
    for (const name of names) {
        const match = source.match(new RegExp(`function ${name}\\([^)]*\\)\\s*\\{[\\s\\S]*?\\n    \\}`));
        assert.ok(match, `${name} must be discoverable`);
        vm.runInContext(match[0], context);
        if (context.page) context.page[name] = context[name];
    }
}

function runHandler(source, name, context) {
    const match = source.match(new RegExp(`${name}:\\s*\\{([\\s\\S]*?)\\n        \\}`));
    assert.ok(match, `${name} must be discoverable`);
    vm.runInContext(`(function() { ${match[1]} })()`, context);
}

module.exports = { loadFunctions, runHandler };
