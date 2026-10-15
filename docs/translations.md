# Translations

`qsTr()` in QML/JS sources, `app/translations/*.ts` sources, compiled
`*.qm` shipped in the RPM and loaded at startup by
`app/src/harbour-electric-eel.cpp` with
`QTranslator::load(QLocale::system(), "harbour-electric-eel", "_", …)`, which
tries each of the system's UI languages from most to least specific (e.g.
`harbour-electric-eel_it_IT`, then `harbour-electric-eel_it`). Untranslated
locales fall back to English.

## Scope

Translated: all QML/JS user-visible strings (pages, menus, dialogs,
command/category labels, model names). Not translated: protocol
identifiers sent to the car (command ids, argument names, enum values),
example placeholders, and Rust/C++ status and error strings (technical
diagnostics).

## Workflow

The per-language `app/translations/harbour-electric-eel_<lang>.ts` files are
the source of truth; edit them directly (or with Qt Linguist). Never generate
them wholesale from scripts: that would overwrite reviewed translations.

`tools/build-qm.sh`:

1. fetches Qt linguist tools from the pinned `PySide6-Essentials` wheel when
   missing (neither the host nor the SDK ship them), cached per version under
   `$XDG_CACHE_HOME/qt-tools/`;
2. runs `lupdate` over `app/qml` (QML and JS) into the template
   `app/translations/harbour-electric-eel.ts`;
3. merges the template into every per-language file
   (`tools/apply-translations.py`): finished translations are kept by
   (context, source), new strings are added as `type="unfinished"`, vanished
   strings are dropped;
4. compiles each language to `*.qm` with `lrelease`.

`tools/build-qm.sh --check` additionally fails if anything under
`app/translations/` differs from the committed state. CI runs it on every push,
so a UI string change must ship with its regenerated `.ts` and `.qm` files.

## Changing UI strings

1. Change the `qsTr()` text in QML/JS.
2. Run `tools/build-qm.sh`. Every language now has the new string marked
   `type="unfinished"` (it falls back to English at runtime).
3. Translate the new `<message>` in each `harbour-electric-eel_<lang>.ts`,
   removing `type="unfinished"`; keep `%1`-style placeholders and protocol
   identifiers (e.g. `presence_inside`) unchanged.
4. Run `tools/build-qm.sh` again and commit the `.ts` and `.qm` files.

## Adding a language

1. Copy `app/translations/harbour-electric-eel.ts` to
   `app/translations/harbour-electric-eel_<lang>.ts` and set
   `language="<lang>"` on the `<TS>` element.
2. Add it to `TRANSLATIONS` in `app/harbour-electric-eel.pro` and update the
   language count in `README.md` and the list below.
3. Translate every `<translation>`, removing `type="unfinished"`.
4. Run `tools/build-qm.sh` and commit the `.ts` and compiled `.qm`.

`app/tests/repo_hygiene_review_test.cjs` checks that every catalog is listed in
the `.pro`, compiled, complete, and counted in the README.

Shipped languages (39): bg bn cs da de el es et fi fr gu hi hu it kn
lt lv ml mr nb nl pa pl pt pt_BR ro ru sk sl sv ta te tr tt uk vi
zh_CN zh_HK zh_TW. Italian was written by hand;
the rest are LLM-generated from English — review by native speakers is
welcome (fix the `.ts`, rerun `tools/build-qm.sh`, commit `.ts`+`.qm`).
