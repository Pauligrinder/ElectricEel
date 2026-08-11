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
// arg.type: "none" | "int" | "float" | "string" | "pin" | "enum"

.pragma library

var CATEGORIES = [
  {
    id: "home",
    title: "Quick Actions",
    commands: [
      cmd("lock", "Lock"),
      cmd("unlock", "Unlock"),
      cmd("honk", "Honk Horn"),
      cmd("flash-lights", "Flash Lights"),
      cmd("wake", "Wake Vehicle"),
      cmd("frunk-open", "Open Front Trunk"),
      cmd("trunk-open", "Open Rear Trunk"),
      cmd("charge-port-open", "Open Charge Port"),
      cmd("charge-port-close", "Close Charge Port"),
    ]
  },
  {
    id: "climate",
    title: "Climate",
    commands: [
      cmd("climate-on", "Climate On"),
      cmd("climate-off", "Climate Off"),
      cmd("climate-set-temp", "Set Temperature", [
        arg("TEMP", "float", { min: 15, max: 28, step: 0.5, unit: "°C", def: 21 })
      ]),
      cmd("seat-heater", "Seat Heater", [
        arg("SEAT", "enum", { values: ["front_left","front_right","rear_left","rear_center","rear_right","third_row_left","third_row_right"] }),
        arg("LEVEL", "int", { min: 0, max: 3, def: 0 })
      ]),
      cmd("steering-wheel-heater", "Steering Wheel Heater", [
        arg("STATE", "enum", { values: ["on","off"] })
      ]),
      cmd("auto-seat-and-climate", "Auto Seat & Climate", [
        arg("POSITIONS", "string", { placeholder: "front_left,front_right" }),
        arg("STATE", "enum", { values: ["on","off"], optional: true })
      ]),
      cmd("precondition-schedule-add", "Add Preconditioning Schedule", scheduleAddArgs()),
      cmd("precondition-schedule-remove", "Remove Preconditioning Schedule", scheduleRemoveArgs()),
    ]
  },
  {
    id: "charging",
    title: "Charging",
    commands: [
      cmd("charging-start", "Start Charging"),
      cmd("charging-stop", "Stop Charging"),
      cmd("charging-set-limit", "Set Charge Limit", [
        arg("PERCENT", "int", { min: 50, max: 100, unit: "%", def: 80 })
      ]),
      cmd("charging-set-amps", "Set Charge Current", [
        arg("AMPS", "int", { min: 1, max: 48, unit: "A", def: 16 })
      ]),
      cmd("charging-schedule", "Schedule Charging", [
        arg("MINS", "int", { min: 0, max: 1439, unit: "min after midnight", def: 0 })
      ]),
      cmd("charging-schedule-cancel", "Cancel Scheduled Charging"),
      cmd("charging-schedule-add", "Add Charge Schedule", scheduleAddArgs()),
      cmd("charging-schedule-remove", "Remove Charge Schedule", scheduleRemoveArgs()),
    ]
  },
  {
    id: "security",
    title: "Locks & Security",
    commands: [
      cmd("lock", "Lock"),
      cmd("unlock", "Unlock"),
      cmd("sentry-mode", "Sentry Mode", [ arg("STATE", "enum", { values: ["on","off"] }) ]),
      cmd("valet-mode-on", "Valet Mode On", [ arg("PIN", "pin", {}) ]),
      cmd("valet-mode-off", "Valet Mode Off"),
      cmd("guest-mode-on", "Guest Mode On"),
      cmd("guest-mode-off", "Guest Mode Off"),
      cmd("erase-guest-data", "Erase Guest Data"),
      cmd("autosecure-modelx", "Auto-Secure (Model X)"),
      cmd("parental-controls-on", "Parental Controls On", [ arg("PIN", "pin", {}) ]),
      cmd("parental-controls-off", "Parental Controls Off", [ arg("PIN", "pin", {}) ]),
      cmd("parental-controls-set-speed-limit", "Set Speed Limit", [
        arg("MPH", "int", { min: 50, max: 90, unit: "mph", def: 70 })
      ]),
      cmd("parental-controls-enable-setting", "Toggle Parental Setting", [
        arg("SETTING", "string", { placeholder: "speed_limit" }),
        arg("STATE", "enum", { values: ["on","off"] })
      ]),
      cmd("parental-controls-clear-pin-admin", "Clear Parental PIN"),
    ]
  },
  {
    id: "body",
    title: "Trunk, Frunk & Windows",
    commands: [
      cmd("trunk-open", "Open Rear Trunk"),
      cmd("trunk-move", "Move Rear Trunk"),
      cmd("trunk-close", "Close Rear Trunk"),
      cmd("frunk-open", "Open Front Trunk"),
      cmd("tonneau-open", "Open Tonneau (Cybertruck)"),
      cmd("tonneau-close", "Close Tonneau (Cybertruck)"),
      cmd("tonneau-stop", "Stop Tonneau (Cybertruck)"),
      cmd("windows-vent", "Vent Windows"),
      cmd("windows-close", "Close Windows"),
    ]
  },
  {
    id: "media",
    title: "Media",
    commands: [
      cmd("media-toggle-playback", "Play / Pause"),
      cmd("media-next-track", "Next Track"),
      cmd("media-previous-track", "Previous Track"),
      cmd("media-next-favorite", "Next Favorite"),
      cmd("media-previous-favorite", "Previous Favorite"),
      cmd("media-volume-up", "Volume Up"),
      cmd("media-volume-down", "Volume Down"),
      cmd("media-set-volume", "Set Volume", [
        arg("VOLUME", "float", { min: 0, max: 11, step: 0.3, def: 5 })
      ]),
    ]
  },
  {
    id: "software",
    title: "Software",
    commands: [
      cmd("software-update-start", "Start Software Update", [
        arg("DELAY", "int", { min: 0, max: 3600, unit: "s", def: 0 })
      ]),
      cmd("software-update-cancel", "Cancel Software Update"),
    ]
  },
  {
    id: "keys",
    title: "Keys",
    commands: [
      cmd("list-keys", "List Enrolled Keys"),
      cmd("add-key", "Add Key", [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
        arg("ROLE", "enum", { values: ["owner","driver"] }),
        arg("FORM_FACTOR", "enum", { values: ["phone_key","cloud_key","nfc_card"] }),
      ]),
      cmd("remove-key", "Remove Key", [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
      ]),
      cmd("rename-key", "Rename Key", [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
        arg("NAME", "string", { placeholder: "My Phone" }),
      ]),
      cmd("session-info", "Session Info", [
        arg("PUBLIC_KEY", "string", { placeholder: "path to public_key.pem" }),
        arg("DOMAIN", "enum", { values: ["vehicle_security","infotainment"] }),
      ]),
    ]
  },
  {
    id: "diagnostics",
    title: "Diagnostics",
    commands: [
      cmd("ping", "Ping Vehicle"),
      // CATEGORY values are cmd/tesla-control's own categoriesByName keys
      // (pkg/vehicle/state.go's StateCategory constants), not the Fleet
      // API's vehicle_data JSON section names - short/hyphenated, not
      // "_state"-suffixed. Verified against the v0.4.1 tag this project
      // pins (see KNOWN_ISSUES.md); a previous guess here used the wrong
      // (Fleet API) names and made every "state" call fail before it ever
      // reached BLE.
      cmd("state", "Get Vehicle State", [
        arg("CATEGORY", "enum", { values: ["charge","climate","drive","location","closures","charge-schedule","precondition-schedule","tire-pressure","media","media-detail","software-update","parental-controls"] })
      ]),
      cmd("body-controller-state", "Body Controller State"),
      cmd("product-info", "Product Info"),
      cmd("keep-accessory-power", "Keep Accessory Power", [ arg("STATE", "enum", { values: ["on","off"] }) ]),
      cmd("low-power-mode", "Low Power Mode", [ arg("STATE", "enum", { values: ["on","off"] }) ]),
    ]
  },
]

function cmd(id, label, args) {
  return { id: id, label: label, args: args || [] }
}

function arg(name, type, opts) {
  opts = opts || {}
  opts.name = name
  opts.type = type
  return opts
}

function scheduleAddArgs() {
  return [
    arg("DAYS", "string", { placeholder: "Mon,Tue,Wed" }),
    arg("TIME", "string", { placeholder: "07:30" }),
    arg("LATITUDE", "float", { min: -90, max: 90, step: 0.000001, def: 0 }),
    arg("LONGITUDE", "float", { min: -180, max: 180, step: 0.000001, def: 0 }),
    arg("REPEAT", "enum", { values: ["once","daily"], optional: true }),
    arg("ID", "int", { min: 0, max: 999, optional: true }),
    arg("ENABLED", "enum", { values: ["true","false"], optional: true }),
  ]
}

function scheduleRemoveArgs() {
  return [
    arg("TYPE", "enum", { values: ["home","work","favorite"] }),
    arg("ID", "int", { min: 0, max: 999, optional: true }),
  ]
}

function findCategory(categoryId) {
  for (var i = 0; i < CATEGORIES.length; i++) {
    if (CATEGORIES[i].id === categoryId)
      return CATEGORIES[i]
  }
  return null
}
