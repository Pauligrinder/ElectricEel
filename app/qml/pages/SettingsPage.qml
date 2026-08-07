import QtQuick 2.6
import Sailfish.Silica 1.0

Page {
    id: page
    property var teslaClient

    property string statusText: ""

    Connections {
        target: teslaClient
        onConfigLoaded: {
            vinField.text = vin
            keyNameField.text = keyName
            connectTimeoutSlider.value = connectTimeoutSec
            commandTimeoutSlider.value = commandTimeoutSec
            page.statusText = hasKey ? "Key on file" : "No key yet - use Pair Vehicle from the main menu"
        }
        onConfigSaved: {
            page.statusText = ok ? "Saved" : ("Save failed: " + errorMessage)
        }
    }

    Component.onCompleted: teslaClient.refreshConfig()

    SilicaFlickable {
        anchors.fill: parent
        contentHeight: column.height

        Column {
            id: column
            width: parent.width
            spacing: Theme.paddingLarge

            PageHeader { title: "Settings" }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                color: Theme.secondaryColor
                font.pixelSize: Theme.fontSizeExtraSmall
                text: page.statusText
            }

            TextField {
                id: vinField
                width: parent.width
                label: "Vehicle VIN"
                placeholderText: "5YJ3E1EA0PF000000"
                EnterKey.iconSource: "image://theme/icon-m-enter-next"
            }

            TextField {
                id: keyNameField
                width: parent.width
                label: "Key name"
                placeholderText: "harbour-teslacontrol"
            }

            Slider {
                id: connectTimeoutSlider
                width: parent.width
                label: "Connect timeout"
                minimumValue: 5
                maximumValue: 60
                stepSize: 1
                valueText: value + " s"
            }

            Slider {
                id: commandTimeoutSlider
                width: parent.width
                label: "Command timeout"
                minimumValue: 2
                maximumValue: 30
                stepSize: 1
                valueText: value + " s"
            }

            Button {
                anchors.horizontalCenter: parent.horizontalCenter
                text: "Save"
                onClicked: teslaClient.setConfig(vinField.text, keyNameField.text,
                                                  connectTimeoutSlider.value, commandTimeoutSlider.value)
            }

            Button {
                anchors.horizontalCenter: parent.horizontalCenter
                text: "Pairing & Keys"
                onClicked: pageStack.push(Qt.resolvedUrl("PairingPage.qml"), { teslaClient: teslaClient })
            }
        }
    }
}
