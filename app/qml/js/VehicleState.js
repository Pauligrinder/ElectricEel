// Parses the protojson output of `tesla-control state <category>` (run via
// TeslaClient.runCommand("state", [category])) into a flat status object the
// dashboard binds to. One category per BLE round-trip - see FirstPage.qml's
// refreshStatus() for why they're chained sequentially rather than fired in
// parallel.
//
// protojson.Format (used by tesla-control, see cmd/tesla-control/state.go)
// omits any field still at its zero value (false/0/"") instead of emitting
// it - so a missing key means "false"/"0", not "unknown". That's indistin-
// guishable from a genuinely-absent field here; merge* below treats both the
// same way (falls back to the zero value), which is the correct read for
// every field currently surfaced except battery level 0%, a real ambiguity
// this CLI wrapper has no way to resolve.

.pragma library

function emptyStatus() {
    return {
        locked: null,
        doorsOpen: false,
        windowsOpen: false,
        insideTemp: null,
        outsideTemp: null,
        isClimateOn: false,
        batteryLevel: null,
        chargingState: "",
        minutesToFullCharge: 0,
        updatedAt: 0
    }
}

function clone(status) {
    var copy = {}
    for (var k in status)
        copy[k] = status[k]
    return copy
}

function mergeClosuresState(status, jsonText) {
    var s = clone(status)
    try {
        var obj = JSON.parse(jsonText)
        s.locked = !!obj.locked
        s.doorsOpen = !!(obj.doorOpenDriverFront || obj.doorOpenDriverRear ||
                          obj.doorOpenPassengerFront || obj.doorOpenPassengerRear ||
                          obj.doorOpenTrunkFront || obj.doorOpenTrunkRear)
        s.windowsOpen = !!(obj.windowOpenDriverFront || obj.windowOpenPassengerFront ||
                            obj.windowOpenDriverRear || obj.windowOpenPassengerRear)
        s.updatedAt = Date.now()
    } catch (e) {
        console.log("VehicleState: closures_state parse failed:", e, jsonText)
    }
    return s
}

function mergeClimateState(status, jsonText) {
    var s = clone(status)
    try {
        var obj = JSON.parse(jsonText)
        s.insideTemp = obj.insideTempCelsius !== undefined ? obj.insideTempCelsius : null
        s.outsideTemp = obj.outsideTempCelsius !== undefined ? obj.outsideTempCelsius : null
        s.isClimateOn = !!obj.isClimateOn
        s.updatedAt = Date.now()
    } catch (e) {
        console.log("VehicleState: climate_state parse failed:", e, jsonText)
    }
    return s
}

function mergeChargeState(status, jsonText) {
    var s = clone(status)
    try {
        var obj = JSON.parse(jsonText)
        s.batteryLevel = obj.batteryLevel !== undefined ? obj.batteryLevel : null
        s.chargingState = obj.chargingState || ""
        s.minutesToFullCharge = obj.minutesToFullCharge || 0
        s.updatedAt = Date.now()
    } catch (e) {
        console.log("VehicleState: charge_state parse failed:", e, jsonText)
    }
    return s
}

// Returns -1 if there's no reading yet, so callers can hide the "updated
// ago" label entirely rather than showing a bogus "NaN minutes ago".
function minutesAgo(timestampMs) {
    if (!timestampMs)
        return -1
    return Math.floor((Date.now() - timestampMs) / 60000)
}
