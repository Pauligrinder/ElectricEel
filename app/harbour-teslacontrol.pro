TARGET = harbour-teslacontrol

CONFIG += sailfishapp
QT += dbus

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
    qml/js/*.js

# The sailfishapp qmake feature auto-installs TARGET.desktop, but icons
# need explicit INSTALLS rules - this is the standard boilerplate every
# Sailfish app template carries.
icon86.files = icons/86x86/harbour-teslacontrol.png
icon86.path = /usr/share/icons/hicolor/86x86/apps
icon108.files = icons/108x108/harbour-teslacontrol.png
icon108.path = /usr/share/icons/hicolor/108x108/apps
icon128.files = icons/128x128/harbour-teslacontrol.png
icon128.path = /usr/share/icons/hicolor/128x128/apps
icon172.files = icons/172x172/harbour-teslacontrol.png
icon172.path = /usr/share/icons/hicolor/172x172/apps

INSTALLS += icon86 icon108 icon128 icon172
