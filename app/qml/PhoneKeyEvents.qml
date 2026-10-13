import QtQuick 2.6
import Nemo.DBus 2.0

// Reuses the session-bus name owned by the main window's Share adaptor.
// No remote command methods: this is an introspectable event source.
Item {
    id: root
    property var client: null

    DBusAdaptor {
        id: eventBus
        bus: DBus.SessionBus
        // Same well-known name as the Share adaptor so Destination-based
        // tooling and introspection agree with path/interface/signal matches.
        service: "org.electriceel.harbour-electric-eel"
        path: "/org/electriceel/PhoneKey"
        iface: "org.electriceel.PhoneKey1"
        xml: '<interface name="org.electriceel.PhoneKey1">'
             + '<signal name="PhoneKeyEvent">'
             + '<arg name="kind" type="s"/>'
             + '<arg name="vin" type="s"/>'
             + '<arg name="time" type="s"/>'
             + '<arg name="error" type="s"/>'
             + '</signal>'
             + '<signal name="PhoneKeyStateChanged">'
             + '<arg name="active" type="b"/>'
             + '<arg name="link" type="s"/>'
             + '<arg name="status" type="s"/>'
             + '</signal></interface>'
    }

    Connections {
        target: root.client
        ignoreUnknownSignals: true
        // Pass strings explicitly: an undefined trailing arg can drop from the
        // D-Bus body, and automagic-daemon then fails required transformations.
        onPhoneKeyEvent: eventBus.emitSignal("PhoneKeyEvent",
                                             [String(kind), String(vin),
                                              String(time), String(errorMessage || "")])
        onPhoneKeyStateChanged: eventBus.emitSignal("PhoneKeyStateChanged",
                                                    [active, String(link), String(status || "")])
    }
}
