// Parses the protojson output of `tesla-control state <category>` (run via
// TeslaClient.runCommand("state", [category])) into a flat status object the
// dashboard binds to. One category per BLE round-trip - see FirstPage.qml's
// refreshStatus() for why they're chained sequentially rather than fired in
// parallel.
//
// Two things that are easy to get wrong here, both confirmed against the
// v0.4.1 tag this project pins (cmd/tesla-control/commands.go,
// pkg/vehicle/state.go, pkg/protocol/protobuf/vehicle.proto) after the
// first version of this file got both wrong at once:
//
// 1. CATEGORY argument values are tesla-control's own short names ("charge",
//    "climate", "closures", ...), not the Fleet API's "_state"-suffixed
//    vehicle_data JSON section names. GetCategory() fails fast on an unknown
//    name (before any BLE I/O), so passing the wrong name looks like the
//    command silently does nothing rather than an obvious error.
// 2. `car.GetState()` returns a `VehicleData` wrapper message, so the
//    parsed JSON is `{"closuresState": {...fields...}}`, not the fields
//    directly at the top level - each merge* function below unwraps its
//    category's field first.
//
// Also, per vehicle.proto: every state category is served over the
// Infotainment domain (car.GetState's own doc comment), same as
// climate/charge commands - unlike lock/unlock and body-controller-state,
// which go through VCSEC and work even while infotainment is asleep. So all
// three legs of the dashboard refresh can fail while lock/unlock keep
// working fine, if the vehicle is asleep. FirstPage.qml surfaces that as a
// hint to wake the vehicle rather than a bare "?".
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
        var obj = JSON.parse(jsonText).closuresState || {}
        s.locked = !!obj.locked
        s.doorsOpen = !!(obj.doorOpenDriverFront || obj.doorOpenDriverRear ||
                          obj.doorOpenPassengerFront || obj.doorOpenPassengerRear ||
                          obj.doorOpenTrunkFront || obj.doorOpenTrunkRear)
        s.windowsOpen = !!(obj.windowOpenDriverFront || obj.windowOpenPassengerFront ||
                            obj.windowOpenDriverRear || obj.windowOpenPassengerRear)
        s.updatedAt = Date.now()
    } catch (e) {
        console.log("VehicleState: closures parse failed:", e, jsonText)
    }
    return s
}

function mergeClimateState(status, jsonText) {
    var s = clone(status)
    try {
        var obj = JSON.parse(jsonText).climateState || {}
        s.insideTemp = obj.insideTempCelsius !== undefined ? obj.insideTempCelsius : null
        s.outsideTemp = obj.outsideTempCelsius !== undefined ? obj.outsideTempCelsius : null
        s.isClimateOn = !!obj.isClimateOn
        s.updatedAt = Date.now()
    } catch (e) {
        console.log("VehicleState: climate parse failed:", e, jsonText)
    }
    return s
}

// ChargingState is a proto oneof-of-empty-messages (vehicle.proto's
// "oneof type { Void Charging = 5; ... }" pattern used for several
// enum-like fields in this schema), not a plain string enum - protojson
// serializes the active variant as an object keyed by the variant's exact
// field name, e.g. {"Charging": {}}, not the string "Charging" or
// "CHARGING". Confirmed empirically (not guessed) by constructing a
// ChargeState_ChargingState_Charging via the v0.4.1 generated Go code and
// running it through protojson.Format directly. This is why a plain
// `obj.chargingState` read used to be compared against a string in
// FirstPage.qml and could never match - not a casing bug, a type one.
function oneofVariantName(oneofObj) {
    if (!oneofObj)
        return ""
    var keys = Object.keys(oneofObj)
    return keys.length ? keys[0] : ""
}

function mergeChargeState(status, jsonText) {
    var s = clone(status)
    try {
        var obj = JSON.parse(jsonText).chargeState || {}
        s.batteryLevel = obj.batteryLevel !== undefined ? obj.batteryLevel : null
        s.chargingState = oneofVariantName(obj.chargingState)
        s.minutesToFullCharge = obj.minutesToFullCharge || 0
        s.updatedAt = Date.now()
    } catch (e) {
        console.log("VehicleState: charge parse failed:", e, jsonText)
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
