# ElectricEel — Sailfish OS GUI for Tesla

Control your Tesla from Sailfish OS over Bluetooth, with a GUI grouped
like the official Tesla app. No account, no servers, no internet: your
phone talks to the car directly over Bluetooth Low Energy.

![electric-eel](./electric-eel.png)

## Features

- Lock/unlock, climate, charging, trunk/frunk/windows, media, software
  updates, keys and diagnostics — the same groups as the Tesla app.
- Phone key: after pairing, the car unlocks on handle-pull and lets you
  drive, like a Tesla phone key. Nothing unlocks or locks by itself.
- Share destinations to the car navigation: from any Sailfish app via
  Share, or copy-paste (including from Android apps) into Send
  Destination. Coordinates navigate exactly; addresses are resolved by
  the car.
- Interface in 39 languages; untranslated locales fall back to English
  (see `docs/translations.md`).
- Two configurable cover quick actions, with rear and front trunk as defaults.

## Requirements

- Sailfish OS phone with Bluetooth, next to the car for pairing and use.
- A Tesla supporting phone keys and an NFC key card (needed once, to
  approve pairing at the center console).

## Install

Download the latest `harbour-electric-eel-*.aarch64.rpm` from
[GitHub releases](https://github.com/nappa85/ElectricEel/releases),
copy it to the phone and install:

```sh
scp harbour-electric-eel-*.aarch64.rpm defaultuser@<phone-ip>:/tmp/
ssh defaultuser@<phone-ip>
devel-su pkcon install-local /tmp/harbour-electric-eel-*.aarch64.rpm
```

## First use

1. Launch ElectricEel → pull down → **Settings** → enter the VIN → Save.
2. Pull down → **Pair Vehicle** → **Generate Key** → **Pair with
   Vehicle** → tap the NFC card on the center console when the car
   prompts. If key generation fails because the stored key is unreadable or
   mismatched, a **Replace Key** button appears; replacing it unpairs this
   phone, so pair again afterwards.
3. Start with read-only commands (Diagnostics → Ping) before actuation
   commands (Lock/Unlock, Climate, Trunk).

To navigate somewhere: pull down → **Send Destination**, paste an
address, coordinates or a map link, preview it, and send. The car must
be in Bluetooth range.

To change the quick buttons in the open-apps grid: **Settings → Cover actions**,
choose the left and right actions, then **Save**. Choices are rear trunk, front
trunk, lock, unlock, flash lights, and horn.

## Documentation

Technical details live in `docs/`:

- [architecture.md](docs/architecture.md): how the app is built
- [build.md](docs/build.md): building, testing, pinned toolchains and releasing
- [translations.md](docs/translations.md): localization workflow
- [navigation-share.md](docs/navigation-share.md): destination sharing
- [phone-key-events.md](docs/phone-key-events.md): D-Bus integration events for other apps
- [automagic.md](docs/automagic.md): Automagic flows for phone-key events
- [tesla-ble-abi.md](docs/tesla-ble-abi.md): the Go protocol library's C ABI
- [vehicle-command-patch.md](docs/vehicle-command-patch.md): Bluetooth protocol additions
- [limitations.md](docs/limitations.md) and [ble-field-test.md](docs/ble-field-test.md): current limits and field-test procedure
- [production-review.md](docs/production-review.md): resolved production review findings

To launch an app or run another automation on phone-key events, follow
[Configure Automagic](docs/automagic.md). It includes an importable example;
Automagic handles the actions.
