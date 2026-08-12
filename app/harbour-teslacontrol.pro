TARGET = harbour-teslacontrol

CONFIG += sailfishapp
QT += dbus

# Single source of the app version, surfaced on the Settings page and
# compared against the helper's GetVersion. The release workflow stamps
# this (and helper/Cargo.toml) from the same git tag, so a matched pair
# reports equal versions. Keep in sync with helper/Cargo.toml when bumping
# outside a release.
VERSION = 0.1.6
DEFINES += APP_VERSION=\\\"$$VERSION\\\"

SOURCES += \
    src/harbour-teslacontrol.cpp \
    src/teslaclient.cpp

HEADERS += \
    src/teslaclient.h

DISTFILES += \
    rpm/harbour-teslacontrol.spec \
    harbour-teslacontrol.desktop \
    qml/harbour-teslacontrol.qml \
    qml/cover/CoverPage.qml \
    qml/pages/*.qml \
    qml/js/*.js \
    img/model3.png \
    img/models.png \
    img/modelx.png \
    img/modely.png \
    img/cybertruck.png \
    img/icons/*.svg

# The sailfishapp qmake feature auto-installs TARGET.desktop and the
# qml/ directory, but launcher icons and the in-app img/ assets used by
# the QML (car graphic + SVG command icons) need explicit INSTALLS rules
# - this is the standard boilerplate every Sailfish app template carries.
# img/ must land under /usr/share/$$TARGET (sibling of qml/) so the
# relative "../../img/..." paths FirstPage.qml / CommandCatalog.js use are
# preserved on-device.
imgdir.files = img
imgdir.path = /usr/share/$${TARGET}

icon86.files = icons/86x86/harbour-teslacontrol.png
icon86.path = /usr/share/icons/hicolor/86x86/apps
icon108.files = icons/108x108/harbour-teslacontrol.png
icon108.path = /usr/share/icons/hicolor/108x108/apps
icon128.files = icons/128x128/harbour-teslacontrol.png
icon128.path = /usr/share/icons/hicolor/128x128/apps
icon172.files = icons/172x172/harbour-teslacontrol.png
icon172.path = /usr/share/icons/hicolor/172x172/apps

INSTALLS += imgdir icon86 icon108 icon128 icon172
