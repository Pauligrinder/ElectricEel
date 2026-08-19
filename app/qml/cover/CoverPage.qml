import QtQuick 2.6
import Sailfish.Silica 1.0
import "../js/VehicleState.js" as VState

CoverBackground {
    id: cover

    property var teslaClient
    property string vin: ""
    property string model: ""
    property bool hasKey: false

    function phoneKeyStatusIsBluetoothOff(status) {
        return status.indexOf("NotPowered") >= 0
            || status.indexOf("RFKILL") >= 0
            || status.indexOf("power on adapter") >= 0
    }

    function phoneKeyStatusIsConnected(status) {
        return status === "Phone key connected"
            || status === "Phone key authorized"
    }

    readonly property bool isPaired: cover.hasKey && cover.vin.length > 0

    readonly property string connectionStatusText: {
        var status = teslaClient ? teslaClient.phoneKeyStatus : ""
        if (phoneKeyStatusIsBluetoothOff(status))
            return "Bluetooth is turned off"
        if (!isPaired)
            return "Unpaired"
        if (phoneKeyStatusIsConnected(status))
            return "Connected"
        return "Disconnected"
    }

    Component.onCompleted: {
        if (teslaClient)
            teslaClient.refreshConfig()
    }

    Connections {
        target: teslaClient
        onConfigLoaded: {
            cover.vin = vin
            cover.model = model
            cover.hasKey = hasKey
        }
    }

    Image {
        id: carImage
        anchors.centerIn: parent
        width: parent.width * 0.85
        height: 72
        fillMode: Image.PreserveAspectFit
        source: VState.carImageSource(cover.model, cover.vin)
        sourceSize: Qt.size(width * 2, height * 2)
        visible: cover.isPaired
    }

    Image {
        id: appIcon
        anchors.centerIn: parent
        width: Theme.iconSizeLarge
        height: width
        fillMode: Image.PreserveAspectFit
        source: "image://theme/icon-size-large?file:///usr/share/icons/hicolor/128x128/apps/harbour-electric-eel.png"
        visible: !cover.isPaired
    }

    Label {
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.bottom: parent.bottom
        anchors.bottomMargin: Theme.paddingMedium
        width: parent.width - 2 * Theme.paddingLarge
        text: cover.connectionStatusText
        wrapMode: Text.WordWrap
        horizontalAlignment: Text.AlignHCenter
        font.pixelSize: Theme.fontSizeSmall
        color: cover.connectionStatusText === "Connected"
               ? Theme.secondaryHighlightColor
               : (cover.connectionStatusText === "Bluetooth is turned off"
                  ? Theme.highlightColor
                  : Theme.secondaryColor)
    }
}
