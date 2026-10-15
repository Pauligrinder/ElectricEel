// Data-driven catalog of tesla-control's subcommands, grouped the way the
// official Tesla app groups its own controls. One generic CategoryPage.qml
// renders whichever category is passed to it from this list, instead of
// hand-writing ~10 nearly-identical pages.
//
// Argument bounds/enum values here are best-effort from the public
// vehicle-command docs and are meant to make entering *valid-looking*
// values easy - tesla-control itself is still the final authority and will
// reject anything it doesn't like. If `tesla-control help <cmd>` on-device
// disagrees with something here, trust the device and fix this file.
//
// arg.type: "int" | "float" | "string" | "pin" | "enum"; optional
// arg.pattern (RegExp) and arg.input: "text" (no slider) refine the form.
//
// arg.stateDefault (optional): name of a VehicleState.js status field whose
// current value should pre-fill this arg's `def` when the dialog opens (the
// slider/field starts at what the vehicle reports, not a hardcoded default).
// CategoryPage.qml clones the arg and applies it - the catalog's own `def`
// stays untouched as a fallback for when status isn't loaded yet.
//
// Toggle pairs (start/stop charging, open/close charge port, climate on/off,
// lock/unlock, vent/close windows, open/close trunk) carry a `visibleWhen`
// predicate that CategoryPage evaluates against the live dashboard
// reading: only the action matching the current vehicle state is listed,
// mirroring the front-page quick actions rule that the UI always reflects
// reality rather than the state a tap would produce. Commands without a
// predicate are always listed.

.pragma library

var CATEGORIES = [
  {
    id: "attention",
    title: qsTr("Attention"),
    icon: "../../img/icons/power.svg",
    commands: [
      cmd("honk", qsTr("Honk Horn")),
      cmd("flash-lights", qsTr("Flash Lights")),
      cmd("wake", qsTr("Wake Vehicle")),
    ]
  },
  {
    id: "climate",
    title: qsTr("Climate"),
    icon: "../../img/icons/fan1.svg",
    commands: [
      cmd("climate-on", qsTr("Climate On"), [], climateOff),
      cmd("climate-off", qsTr("Climate Off"), [], climateOn),
      // sendSuffix "C": tesla-control's climate-set-temp expects the unit
      // glued directly onto the number (22C or 72F) - a bare number always
      // failed to parse. See ArgumentDialog.qml's sliderField.
      cmd("climate-set-temp", qsTr("Set Temperature"), [
        arg("TEMP", "float", { min: 15, max: 28, step: 0.5, unit: "°C", sendSuffix: "C", def: 21, stateDefault: "driverTempSetting" })
      ]),
      // SEAT/LEVEL values are commands_vendor.go's own seats/levels map
      // keys verbatim - hyphenated positions, off|low|medium|high levels
      // (not the Fleet-API-flavored underscore names or a numeric level
      // this previously guessed).
      cmd("seat-heater", qsTr("Seat Heater"), [
        arg("SEAT", "enum", { values: ["front-left","front-right","2nd-row-left","2nd-row-center","2nd-row-right","3rd-row-left","3rd-row-right"] }),
        arg("LEVEL", "enum", { values: ["off","low","medium","high"] })
      ]),
      cmd("steering-wheel-heater", qsTr("Steering Wheel Heater"), [
        arg("STATE", "enum", { values: ["on","off"] })
      ]),
      // POSITIONS is 'L', 'R', or 'LR' verbatim (commands_vendor.go checks
      // strings.Contains for each letter) - not a comma-separated seat list.
      cmd("auto-seat-and-climate", qsTr("Auto Seat & Climate"), [
        arg("POSITIONS", "enum", { values: ["L","R","LR"] }),
        arg("STATE", "enum", { values: ["on","off"], optional: true })
      ]),
      cmd("precondition-schedule-add", qsTr("Add Preconditioning Schedule"), preconditionScheduleAddArgs()),
      cmd("precondition-schedule-remove", qsTr("Remove Preconditioning Schedule"), scheduleRemoveArgs()),
    ]
  },
  {
    id: "charging",
    title: qsTr("Charging"),
    icon: "../../img/icons/bolt.svg",
    commands: [
      cmd("charging-start", qsTr("Start Charging"), [], notCharging),
      cmd("charging-stop", qsTr("Stop Charging"), [], charging),
      cmd("charge-port-open", qsTr("Open Charge Port"), [], portClosed),
      cmd("charge-port-close", qsTr("Close Charge Port"), [], portOpen),
      cmd("charging-set-limit", qsTr("Set Charge Limit"), [
        arg("PERCENT", "int", { min: 50, max: 100, unit: "%", def: 80, stateDefault: "chargeLimitSoc" })
      ]),
      cmd("charging-set-amps", qsTr("Set Charge Current"), [
        arg("AMPS", "int", { min: 1, max: 48, unit: "A", def: 16, stateDefault: "chargeCurrent" })
      ]),
      cmd("charging-schedule", qsTr("Schedule Charging"), [
        arg("MINS", "int", { min: 0, max: 1439, unit: "min after midnight", def: 0 })
      ]),
      cmd("charging-schedule-cancel", qsTr("Cancel Scheduled Charging")),
      cmd("charging-schedule-add", qsTr("Add Charge Schedule"), chargeScheduleAddArgs()),
      cmd("charging-schedule-remove", qsTr("Remove Charge Schedule"), scheduleRemoveArgs()),
    ]
  },
  {
    id: "security",
    title: qsTr("Locks & Security"),
    icon: "../../img/icons/security.svg",
    commands: [
      cmd("lock", qsTr("Lock"), [], unlockedState),
      cmd("unlock", qsTr("Unlock"), [], lockedState),
      // drive = remote start / keyless drive (RKE_ACTION_REMOTE_DRIVE): lets
      // a keyless driver drive for a short window after unlock. Distinct from
      // phone-key driving, which needs no command at all - the car authorizes
      // it from an authenticated VCSEC session with a valid key. Kept in
      // Locks & Security as it's a drive-authorization action.
      cmd("drive", qsTr("Remote Start (Keyless Drive)"), []),
      cmd("sentry-mode", qsTr("Sentry Mode"), [ arg("STATE", "enum", { values: ["on","off"] }) ]),
      // The vehicle only accepts a four-digit valet PIN.
      cmd("valet-mode-on", qsTr("Valet Mode On"), [ arg("PIN", "pin", { pattern: /^\d{4}$/, placeholder: "4 digits" }) ]),
      cmd("valet-mode-off", qsTr("Valet Mode Off")),
      cmd("guest-mode-on", qsTr("Guest Mode On")),
      cmd("guest-mode-off", qsTr("Guest Mode Off")),
      cmd("erase-guest-data", qsTr("Erase Guest Data")),
      cmd("autosecure-modelx", qsTr("Auto-Secure (Model X)")),
      // parental-controls-* commands used to be listed here, but they don't
      // exist in cmd/tesla-control/commands.go at the pinned v0.4.1 tag -
      // "parental-controls" is only a `state` CATEGORY name (read-only
      // status query), not a family of action commands. Every one of these
      // would fail with "unrecognized command" before ever reaching BLE.
    ]
  },
  {
    id: "body",
    title: qsTr("Trunk, Frunk & Windows"),
    icon: "../../img/icons/window.svg",
    commands: [
      cmd("trunk-open", qsTr("Open Rear Trunk"), [], trunkClosed),
      cmd("trunk-move", qsTr("Move Rear Trunk")),
      cmd("trunk-close", qsTr("Close Rear Trunk"), [], trunkOpen),
      cmd("frunk-open", qsTr("Open Front Trunk")),
      cmd("tonneau-open", qsTr("Open Tonneau (Cybertruck)")),
      cmd("tonneau-close", qsTr("Close Tonneau (Cybertruck)")),
      cmd("tonneau-stop", qsTr("Stop Tonneau (Cybertruck)")),
      cmd("windows-vent", qsTr("Vent Windows"), [], windowsClosed),
      cmd("windows-close", qsTr("Close Windows"), [], windowsOpen),
    ]
  },
  {
    id: "media",
    title: qsTr("Media"),
    icon: "../../img/icons/navigation.svg",
    commands: [
      cmd("media-toggle-playback", qsTr("Play / Pause")),
      cmd("media-next-track", qsTr("Next Track")),
      cmd("media-previous-track", qsTr("Previous Track")),
      cmd("media-next-favorite", qsTr("Next Favorite")),
      cmd("media-previous-favorite", qsTr("Previous Favorite")),
      cmd("media-volume-up", qsTr("Volume Up")),
      cmd("media-volume-down", qsTr("Volume Down")),
      cmd("media-set-volume", qsTr("Set Volume"), [
        arg("VOLUME", "float", { min: 0, max: 10, step: 0.5, def: 5 })
      ]),
    ]
  },
  {
    id: "software",
    title: qsTr("Software"),
    icon: "../../img/icons/upgrades.svg",
    commands: [
      // sendSuffix "s": upstream parses DELAY with Go's time.ParseDuration,
      // which requires a unit (10m, 2h, ...) - a bare integer only ever
      // worked by accident at DELAY=0, the one value ParseDuration accepts
      // unitless.
      cmd("software-update-start", qsTr("Start Software Update"), [
        arg("DELAY", "int", { min: 0, max: 3600, unit: "s", sendSuffix: "s", def: 0 })
      ]),
      cmd("software-update-cancel", qsTr("Cancel Software Update")),
    ]
  },
  {
    id: "keys",
    title: qsTr("Keys"),
    icon: "../../img/icons/lock.svg",
    commands: [
      cmd("list-keys", qsTr("List Enrolled Keys")),
      // FORM_FACTOR is vcsec.KeyFormFactor's own value names (minus the
      // KEY_FORM_FACTOR_ prefix, case-insensitive) - "phone_key" isn't one
      // of them. This app's own pairing (helper/src/core.rs) enrolls as
      // android_device, a phone form factor the vehicle treats as a real
      // drive-authorizing key (cloud_key would remote-control but not
      // authorize driving).
      cmd("add-key", qsTr("Add Key"), [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
        arg("ROLE", "enum", { values: ["owner","driver"] }),
        arg("FORM_FACTOR", "enum", { values: ["nfc_card","ios_device","android_device","cloud_key"] }),
      ]),
      cmd("remove-key", qsTr("Remove Key"), [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
      ]),
      // rename-key omitted: requiresFleetAPI is true
      // (commands_vendor.go:438) and this app is BLE-only, so it always
      // fails with "command requires a FleetAPI OAuth token" - no point
      // offering a button guaranteed to error.
      cmd("session-info", qsTr("Session Info"), [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
        arg("DOMAIN", "enum", { values: ["vcsec","infotainment"] }),
      ]),
    ]
  },
  {
    id: "diagnostics",
    title: qsTr("Diagnostics"),
    icon: "../../img/icons/service.svg",
    commands: [
      cmd("ping", qsTr("Ping Vehicle")),
      // CATEGORY values are cmd/tesla-control's own categoriesByName keys
      // (pkg/vehicle/state.go's StateCategory constants), not the Fleet
      // API's vehicle_data JSON section names - short/hyphenated, not
      // "_state"-suffixed. Verified against the v0.4.1 tag this project
      // pins (see docs/limitations.md); a previous guess here used the wrong
      // (Fleet API) names and made every "state" call fail before it ever
      // reached BLE.
      cmd("state", qsTr("Get Vehicle State"), [
        arg("CATEGORY", "enum", { values: ["charge","climate","drive","location","closures","charge-schedule","precondition-schedule","tire-pressure","media","media-detail","software-update","parental-controls"] })
      ]),
      cmd("body-controller-state", qsTr("Body Controller State")),
      // product-info omitted: requiresFleetAPI is true
      // (commands_vendor.go:963), always fails the same way rename-key
      // does in this BLE-only app.
      cmd("keep-accessory-power", qsTr("Keep Accessory Power"), [ arg("STATE", "enum", { values: ["on","off"] }) ]),
      cmd("low-power-mode", qsTr("Low Power Mode"), [ arg("STATE", "enum", { values: ["on","off"] }) ]),
    ]
  },
]

function cmd(id, label, args, visibleWhen) {
  return { id: id, label: label, args: args || [], visibleWhen: visibleWhen }
}

function arg(name, type, opts) {
  opts = opts || {}
  opts.name = name
  opts.type = type
  return opts
}

// visibleWhen predicates. Each receives the dashboard's status object and
// returns whether its command row should be listed. Unknown states
// (null/"") deliberately fall to the "not active" reading so the forward
// action (climate on, start charging, ...) stays reachable instead of
// leaving a category empty; unknown windows offer Close, the safe action. See VehicleState.js for the
// exact field names (protojson camelCase of vehicle.proto's fields, and
// chargingState is a oneof VARIANT name - e.g. "Charging" - not a plain string).
function climateOn(s)  { return !!s.isClimateOn }
function climateOff(s) { return !s.isClimateOn }
function nearlyCharging(s) { return s.chargingState === "Charging" || s.chargingState === "Starting" || s.chargingState === "Calibrating" }
function charging(s)   { return nearlyCharging(s) }
function notCharging(s){ return !nearlyCharging(s) }
function portOpen(s)   { return !!s.chargePortOpen }
function portClosed(s) { return !s.chargePortOpen }
function lockedState(s)  { return s.locked !== false }
function unlockedState(s){ return s.locked === false }
// windowsOpen is null while unknown: offer Close (the safe action) then.
function windowsOpen(s)  { return s.windowsOpen !== false }
function windowsClosed(s){ return s.windowsOpen === false }
function trunkOpen(s)  { return !!s.trunkRearOpen }
function trunkClosed(s){ return !s.trunkRearOpen }

// DAYS is GetDays()'s own dayNamesBitMask keys (case-insensitive): Sun,
// Mon, Tues, Wed, Thurs, Fri, Sat, or all/weekdays - note "Tues"/"Thurs",
// not "Tue"/"Thu".
// Optional values retain their backend positions. Empty slots mean omitted;
// only trailing empty slots can be removed from the wire argument list.
// Both *-schedule-add commands honor ID (helper/src/protocol_commands.rs
// reads args[5]): blank creates a new schedule, an existing ID replaces it.
// Coordinates are typed (input: "text"): a slider over +-90/+-180 degrees
// cannot hit a real location.
function chargeScheduleAddArgs() {
  return [
    arg("DAYS", "string", { placeholder: "Mon,Tues,Wed" }),
    arg("TIME", "string", { placeholder: "22:00-06:00 (start or end may be blank)" }),
    coordinateArg("LATITUDE", 90, "45.464211"),
    coordinateArg("LONGITUDE", 180, "9.191383"),
    arg("REPEAT", "enum", { values: ["once"], optional: true }),
    scheduleIdArg(),
    arg("ENABLED", "enum", { values: ["true","false"], optional: true }),
  ]
}

// Same shape as chargeScheduleAddArgs() except TIME: preconditioning has a
// single trigger time, not a start-end range.
function preconditionScheduleAddArgs() {
  return [
    arg("DAYS", "string", { placeholder: "Mon,Tues,Wed" }),
    arg("TIME", "string", { placeholder: "22:00" }),
    coordinateArg("LATITUDE", 90, "45.464211"),
    coordinateArg("LONGITUDE", 180, "9.191383"),
    arg("REPEAT", "enum", { values: ["once"], optional: true }),
    scheduleIdArg(),
    arg("ENABLED", "enum", { values: ["true","false"], optional: true }),
  ]
}

function coordinateArg(name, limit, placeholder) {
  return arg(name, "float", { min: -limit, max: limit, input: "text", placeholder: placeholder })
}

// Schedule IDs are unsigned on the wire (protocol_commands.rs unsigned()).
function scheduleIdArg() {
  return arg("ID", "int", { min: 0, placeholder: "leave blank for a new schedule", optional: true })
}

function scheduleRemoveArgs() {
  return [
    // home|work|other remove by category; "id" removes one schedule by
    // its numeric ID (see list-keys-style ID from the *-add commands'
    // output). "favorite" was never a real TYPE value.
    arg("TYPE", "enum", { values: ["home","work","other","id"] }),
    arg("ID", "int", { min: 0, placeholder: "required when TYPE is id", optional: true }),
  ]
}

// Commands whose effect shows on the dashboard (lock, climate, closures,
// charge readings, or a woken infotainment). Only these ask Rust for the
// delayed post-command status refresh; honk, media, keys and diagnostics
// would just occupy the serial BLE queue with four status reads.
var STATUS_COMMANDS = [
  "wake", "climate-on", "climate-off", "climate-set-temp", "auto-seat-and-climate",
  "charging-start", "charging-stop", "charge-port-open", "charge-port-close",
  "charging-set-limit", "charging-set-amps", "lock", "unlock",
  "trunk-open", "trunk-move", "trunk-close", "frunk-open", "windows-vent", "windows-close",
]

function refreshesStatus(commandId) {
  return STATUS_COMMANDS.indexOf(commandId) >= 0
}

function findCategory(categoryId) {
  for (var i = 0; i < CATEGORIES.length; i++) {
    if (CATEGORIES[i].id === categoryId)
      return CATEGORIES[i]
  }
  return null
}
