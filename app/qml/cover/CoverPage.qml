import QtQuick 2.6
import Sailfish.Silica 1.0
import "../js/VehicleState.js" as VState
import "../js/PhoneKeyState.js" as PhoneKey
import "../js/CoverActions.js" as CoverActions

CoverBackground {
    id: cover

    property var teslaClient
    property string vin: ""
    property string model: ""
    property bool hasKey: false
    property bool commandBusy: false
    property string pendingCmd: ""
    property string configError: ""
    property string leftAction: "trunk-open"
    property string rightAction: "frunk-open"

    readonly property bool isPaired: cover.hasKey && cover.vin.length > 0

    // unpaired | bluetooth-off | connected | disconnected
    readonly property string connectionKind: PhoneKey.connectionKind(
        teslaClient ? teslaClient.phoneKeyLink : "stopped", isPaired)

    readonly property bool isConnected: connectionKind === "connected"
    // A command can establish its own link while presence is scanning.
    // Use the dashboard's key/VIN prerequisites, not the connection icon.
    readonly property bool canCommand: !!teslaClient && teslaClient.helperAvailable && isPaired && configError.length === 0

    function runCoverCommand(cmd) {
        if (!cover.canCommand || cover.commandBusy)
            return
        cover.commandBusy = true
        cover.pendingCmd = teslaClient.newRequestId("cover:" + cmd, cover.vin)
        teslaClient.runCommand(cover.pendingCmd, cmd, [])
    }

    Component.onCompleted: {
        if (teslaClient)
            teslaClient.refreshConfig()
    }

    Connections {
        target: teslaClient
        onConfigLoaded: {
            cover.configError = ""
            if (cover.vin !== vin) {
                cover.pendingCmd = ""
                cover.commandBusy = false
            }
            cover.vin = vin
            cover.model = model
            cover.hasKey = hasKey
            cover.leftAction = CoverActions.action(coverLeftAction, 0).id
            cover.rightAction = CoverActions.action(coverRightAction, 1).id
        }
        onConfigLoadError: cover.configError = message
        onCommandFinished: {
            if (requestId === cover.pendingCmd) {
                cover.commandBusy = false
                cover.pendingCmd = ""
            }
        }
        onCommandError: {
            if (requestId === cover.pendingCmd) {
                cover.commandBusy = false
                cover.pendingCmd = ""
            }
        }
    }

    CoverActionList {
        enabled: cover.canCommand && !cover.commandBusy

        CoverAction {
            iconSource: Qt.resolvedUrl(CoverActions.action(cover.leftAction, 0).icon)
            onTriggered: cover.runCoverCommand(cover.leftAction)
        }

        CoverAction {
            iconSource: Qt.resolvedUrl(CoverActions.action(cover.rightAction, 1).icon)
            onTriggered: cover.runCoverCommand(cover.rightAction)
        }
    }

    // Tesla "T" watermark: large but readable, anchored bottom-right with a
    // slice spilling off the edge (not so big that only an unrecognizable
    // fragment remains on the cover).
    Image {
        id: logoWatermark
        height: parent.height * 1.45
        width: height * 254.584 / 253.502
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.rightMargin: -width * 0.28
        anchors.bottomMargin: -height * 0.30
        opacity: 0.22
        z: 0
        source: Qt.resolvedUrl("../../img/icons/tesla_logo.svg")
        fillMode: Image.PreserveAspectFit
        sourceSize: Qt.size(width * 2, height * 2)
        clip: false
    }

    Column {
        anchors.fill: parent
        anchors.margins: Theme.paddingMedium
        // Leave room for the native cover actions drawn over the cover.
        anchors.bottomMargin: Theme.paddingMedium + (cover.canCommand ? cover.coverActionArea.height : 0)
        spacing: Theme.paddingSmall
        z: 1

        Label {
            id: configErrorLabel
            width: parent.width
            visible: cover.configError.length > 0
            height: visible ? implicitHeight : 0
            wrapMode: Text.Wrap
            font.pixelSize: Theme.fontSizeExtraSmall
            text: cover.configError + "\n" + qsTr("Retry")
            MouseArea {
                anchors.fill: parent
                onClicked: teslaClient.refreshConfig()
            }
        }

        // Status badge: filled glyphs on a dark disc so they read against
        // the Tesla watermark. Not an IconButton / MouseArea.
        Item {
            id: statusSlot
            width: parent.width
            visible: cover.connectionKind !== "unpaired"
            height: visible ? statusBadge.height : 0

            Item {
                id: statusBadge
                anchors.horizontalCenter: parent.horizontalCenter
                width: Theme.iconSizeMedium + Theme.paddingSmall * 2
                height: width

                Rectangle {
                    anchors.fill: parent
                    radius: width / 2
                    color: Theme.rgba("#000000", 0.62)
                }

                HighlightImage {
                    id: statusIcon
                    anchors.centerIn: parent
                    width: Theme.iconSizeMedium
                    height: Theme.iconSizeMedium
                    sourceSize: Qt.size(width, height)
                    color: cover.connectionKind === "connected" ? "#4CD964" : "#FF453A"
                    source: {
                        if (cover.connectionKind === "connected")
                            return Qt.resolvedUrl("../../img/icons/wifi.svg")
                        if (cover.connectionKind === "bluetooth-off")
                            return Qt.resolvedUrl("../../img/icons/bluetooth_disabled.svg")
                        return Qt.resolvedUrl("../../img/icons/wifi_off.svg")
                    }
                }
            }
        }

        Item {
            width: parent.width
            height: Math.max(0, parent.height - statusSlot.height - configErrorLabel.height
                    - parent.spacing * (1 + (configErrorLabel.visible ? 1 : 0)))

            Image {
                id: carImage
                anchors.fill: parent
                fillMode: Image.PreserveAspectFit
                source: VState.carImageSource(cover.model, cover.vin)
                sourceSize: Qt.size(width * 2, height * 2)
                visible: cover.isPaired
            }

            Image {
                id: appIcon
                anchors.centerIn: parent
                width: Math.min(parent.width * 0.55, parent.height * 0.55)
                height: width
                fillMode: Image.PreserveAspectFit
                source: "image://theme/icon-size-large?file:///usr/share/icons/hicolor/128x128/apps/harbour-electric-eel.png"
                visible: !cover.isPaired
            }
        }
    }
}
