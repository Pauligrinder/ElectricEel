# Phone-key BLE field test (no terminal needed at the car)

This captures a baseline for disconnected scanning, approach, passive entry,
and departure. Read the entire sequence before going to the car. The app
writes timestamps and radio diagnostics automatically; no commands, root
access, or live instructions are needed during the walk.

## Before leaving the desk

1. Install the diagnostic RPM through Sailfish's normal package/file-manager
   interface if it accepts local RPM upgrades. Close ElectricEel completely
   and reopen it so the new session child runs. Check that your VIN and key
   are still present in Settings; do not generate a new key.
2. Enable Bluetooth, leave ElectricEel open (background/cover is fine), and
   check that it says **Phone key scanning**. Carry the NFC key card in case
   passive entry fails. Don't turn off Bluetooth, restart services, or use a
   separate Bluetooth scanner during the baseline.
3. Note the start time on your phone clock. Make sure you can access
   `Documents/ElectricEel/phone-key-YYYY-MM-DD.log` in Sailfish File Browser.
   A new file should contain `session`/`presence` and `bluez` lines. If the
   file is absent, stop here and tell us; we can arrange another collection
   method before the walk.

## Walk (memorize: FAR 3 → NEAR 2 → HANDLE → AWAY 3)

1. **FAR — 3 minutes.** Stay well out of car range (ideally 30+ metres with
   walls between you and the car). Keep the app running; do not open Tesla's
   official app on another device. This measures idle discovery pressure.
2. **NEAR — 2 minutes.** Walk normally to the closed, locked car. Stand near
   the driver door without touching it for about 2 minutes. Note roughly
   when you arrived; the log records connection timing independently.
3. **HANDLE.** Pull the handle once. Note whether it unlocks on the first
   try and approximate delay; if it fails, wait 10 seconds and try once more.
   Use the NFC card if needed. Open and close the door normally. Do not issue
   a manual Lock/Unlock from the app during this first pass.
4. **AWAY — 3 minutes.** Walk back to the FAR location and wait. If the car's
   own walk-away lock is enabled, note whether it locks; the app does not
   initiate locking. Don't restart Bluetooth even if phone key stops working.

If practical, repeat the same walk **once** with the phone screen off and the
app in the background. Note that this is the screen-off pass; it distinguishes
suspend/resume problems from ordinary scanning. If a failure occurs, note its
time, whether other Bluetooth devices still work, and whether opening the app
shows an error. Preserve the failed state and the log; end the test rather
than repeatedly trying to reconnect.

## Back at the desk

Copy or share that day's `phone-key-YYYY-MM-DD.log` from
`Documents/ElectricEel/`. Include approximate times for arrival, handle
pull(s), departure, and any failure, plus the app's status text. The log can
contain the VIN, so redact it if you prefer before sharing. If the test
crossed midnight, include both days. No system log or privileged command is
required for this first pass; ask before running any command on the phone.

The `bluez` scan-window lines count full object-tree reads, beacon/RSSI
snapshots, RSSI `PropertiesChanged` signals, and their total/maximum read
time. RSSI snapshot counts are **not** counts of fresh radio advertisements:
BlueZ can retain RSSI on a cached device. An `rssiUpdateSignals=0` window
does not prove the radio heard nothing (BlueZ need not emit a signal for every
advertisement), but a nonzero count confirms actual updates reached the app.
`lastRSSIUpdateAge=-1ns` means no RSSI signal has been observed yet. The `ui`
lines show Qt application-state transitions; screen-off might not trigger a
Qt state change. `presence loop gap` marks an interval when the session child
was not advancing; it cannot by itself distinguish suspension from a blocked
call. Connect-attempt and discovery-transition lines show where retries or
timeouts occurred.
