#!/usr/bin/env bash
# Build Qt message catalogs for the Harbour QML UI.
#
# qsTr() in QML/JS, translations/*.ts sources, compiled *.qm shipped in
# the RPM and loaded per locale by main.cpp. Neither the host nor the
# SailfishOS SDK ship linguist tools, so lupdate/lrelease come from the PySide6-Essentials
# wheel (pip download only — no system Qt needed), cached under
# $XDG_CACHE_HOME/qt-tools/pyside6-essentials-<version> alongside the Qt libs they resolve via
# RUNPATH $ORIGIN/Qt/lib.
#
# Usage:
#   tools/build-qm.sh            # refresh translations/*.ts + rebuild *.qm
#   tools/build-qm.sh --check    # CI gate: fail if committed files differ
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]:-..}")/.." && pwd)"
# Pinned: the --check gate compares byte-for-byte, so a different
# lupdate/lrelease release could change the generated catalogs. 6.12.0
# reproduces the committed app/translations. Bump deliberately, rerun this
# script and commit the regenerated catalogs in the same change.
pyside_requirement="PySide6-Essentials==6.12.0"
pyside_version="${pyside_requirement#*==}"
tools_dir="${XDG_CACHE_HOME:-$HOME/.cache}/qt-tools/pyside6-essentials-$pyside_version"
lupdate="$tools_dir/PySide6/lupdate"
lrelease="$tools_dir/PySide6/lrelease"
check_only=0
if [ "${1:-}" = "--check" ]; then
    check_only=1
fi

if [ ! -x "$lupdate" ] || [ ! -x "$lrelease" ]; then
    echo "Fetching Qt linguist tools (PySide6-Essentials==$pyside_version wheel)..."
    wheel_dir="$(mktemp -d)"
    trap 'rm -rf "$wheel_dir"' EXIT
    mkdir -p "$tools_dir"
    pip download --no-deps --only-binary=:all: --dest "$wheel_dir" "$pyside_requirement"
    rm -rf "$tools_dir/PySide6"
    WHEEL_DIR="$wheel_dir" TOOLS_DIR="$tools_dir" python3 -c "
import glob, os, sys, zipfile
wheels = glob.glob(os.path.join(os.environ['WHEEL_DIR'], '*.whl'))
if len(wheels) != 1:
    sys.exit(f'expected exactly one wheel, found {wheels}')
z = zipfile.ZipFile(wheels[0])
names = [n for n in z.namelist()
         if n in ('PySide6/lupdate', 'PySide6/lrelease')
         or n.startswith('PySide6/Qt/lib/')]
z.extractall(os.environ['TOOLS_DIR'], members=names)
"
    chmod +x "$lupdate" "$lrelease"
fi

# Smoke test with stderr visible: a broken toolchain (e.g. unresolvable
# bundled libs) must fail here with its loader error, not later as a bare
# exit 127 with output suppressed.
"$lupdate" -version

cd "$repo_root"
# shellcheck disable=SC2207
sources=($(find app/qml -name '*.qml' -o -name '*.js' | sort))
"$lupdate" "${sources[@]}" -ts app/translations/harbour-electric-eel.ts
python3 tools/apply-translations.py
shopt -s nullglob
ts_files=(app/translations/harbour-electric-eel_*.ts)
if [ ${#ts_files[@]} -eq 0 ]; then
    echo "no language files yet — template only (copy it to app/translations/harbour-electric-eel_<lang>.ts to start one)"
else
    for ts in "${ts_files[@]}"; do
        "$lrelease" "$ts" 2>&1 | tail -n 2
    done
fi

if [ "$check_only" = "1" ]; then
    # Any change under translations/ (modified OR untracked — a fresh
    # language must be committed too) fails the gate.
    if [ -n "$(git status --porcelain -- app/translations/)" ]; then
        echo "ERROR: translations/ out of sync — run tools/build-qm.sh and commit"
        git status --short -- app/translations/
        exit 1
    fi
fi
echo "Catalogs ready: $(ls app/translations/*.qm 2>/dev/null || echo none)"
