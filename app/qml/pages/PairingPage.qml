import QtQuick 2.6
import Sailfish.Silica 1.0
import "../js/CommandText.js" as CommandText
import "../js/PhoneKeyState.js" as PhoneKey

Page {
    id: page
    property var teslaClient

    // Configuration comes from TeslaClient. configReady gates Generate Key:
    // clicking it before the load finished would run with no knowledge of
    // an already-enrolled key.
    readonly property string publicKeyPem: teslaClient ? teslaClient.publicKeyPem : ""
    readonly property string vin: teslaClient ? teslaClient.vin : ""
    readonly property bool hasVin: vin.length > 0
    readonly property bool configReady: teslaClient ? teslaClient.configReady : false
    property bool generating: false
    property bool pairing: false
    // Set after a failed generation: an unreadable or mismatched key can only
    // be recovered by replacing it (generateKey(true)).
    property bool keyFailed: false
    property string pairStatus: ""
    property string keysListOutput: ""
    // Per-page list-keys id so two PairingPages never consume each other's
    // replies.
    property string pendingListKeysId: ""

    Connections {
        target: teslaClient
        onKeyGenerated: {
            page.generating = false
            page.keyFailed = !ok
            // TeslaClient republishes hasKey/publicKeyPem from the new config.
            teslaClient.refreshConfig()
            if (ok) {
                page.pairStatus = qsTr("Key generated. Tap \"Pair with Vehicle\", then tap your NFC card on the center console when prompted on the car's screen.")
            } else {
                page.pairStatus = qsTr("Key generation failed: %1").arg(errorMessage)
            }
        }
        onPaired: {
            page.pairing = false
            page.pairStatus = ok ? (qsTr("Paired.") + "\n" + output) : qsTr("Pairing failed: %1").arg(errorMessage)
            if (ok)
                teslaClient.refreshConfig()
        }
        onCommandFinished: {
            if (requestId !== page.pendingListKeysId)
                return
            page.pendingListKeysId = ""
            page.keysListOutput = ok
                ? (stdOut.length > 0 ? stdOut : qsTr("No keys listed"))
                : CommandText.failure(stdErr, exitCode)
        }
        onCommandError: {
            if (requestId !== page.pendingListKeysId)
                return
            page.pendingListKeysId = ""
            page.keysListOutput = message
        }
    }

    // A pending list belongs to the previous car (its ID carries that VIN).
    onVinChanged: {
        page.pendingListKeysId = ""
        page.keysListOutput = ""
    }

    function generate(force) {
        page.generating = true
        page.keyFailed = false
        teslaClient.generateKey(force)
    }

    RemorsePopup { id: replaceRemorse }

    SilicaFlickable {
        anchors.fill: parent
        contentHeight: column.height

        Column {
            id: column
            width: parent.width
            spacing: Theme.paddingLarge

            PageHeader { title: qsTr("Pairing & Keys") }

            ConfigErrorBanner { client: teslaClient }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                text: qsTr("Set the VIN in Settings first. Then generate a key, then pair it with the car over BLE - you'll need to be next to the vehicle and tap the NFC card on the center console to approve.")
                font.pixelSize: Theme.fontSizeExtraSmall
                color: Theme.secondaryColor
            }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                text: teslaClient.phoneKeyStatus.length > 0
                      ? teslaClient.phoneKeyStatus
                      : qsTr("Phone key starting...")
                font.pixelSize: Theme.fontSizeSmall
                color: PhoneKey.isError(teslaClient.phoneKeyLink)
                       ? Theme.highlightColor
                       : Theme.secondaryHighlightColor
            }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                text: qsTr("Phone-key logs: %1").arg("Documents/ElectricEel/phone-key-YYYY-MM-DD.log")
                font.pixelSize: Theme.fontSizeExtraSmall
                color: Theme.secondaryColor
            }

            BusyIndicator {
                anchors.horizontalCenter: parent.horizontalCenter
                // Spins until the first GetConfig reply lands (configReady
                // gates the Generate Key button) so the disabled button reads
                // as "still loading" rather than "broken".
                running: !page.configReady && teslaClient.configError.length === 0
                visible: running
            }

            Button {
                anchors.horizontalCenter: parent.horizontalCenter
                text: page.generating ? qsTr("Generating...") : qsTr("Generate Key")
                // Gated on configReady: clicking Generate Key before the
                // config loads would run without knowing a key already
                // exists. Also disabled while pairing - generating during the
                // in-flight add-key-request would invalidate the session
                // mid-pairing.
                enabled: page.configReady && !page.generating && !page.pairing && page.pendingListKeysId.length === 0
                onClicked: page.generate(false)
            }

            // Recovery for a key the core refused to reuse (unreadable or
            // mismatched files). Replacing unpairs this phone, so it is
            // confirmed with a cancellable remorse timer.
            Button {
                anchors.horizontalCenter: parent.horizontalCenter
                visible: page.keyFailed
                text: qsTr("Replace Key")
                enabled: page.configReady && !page.generating && !page.pairing && page.pendingListKeysId.length === 0
                onClicked: replaceRemorse.execute(qsTr("Replacing key"), function() { page.generate(true) })
            }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.WrapAnywhere
                visible: page.publicKeyPem.length > 0
                font.pixelSize: Theme.fontSizeExtraSmall
                font.family: "monospace"
                text: page.publicKeyPem
            }

            Button {
                anchors.horizontalCenter: parent.horizontalCenter
                text: page.pairing ? qsTr("Waiting for NFC tap...") : qsTr("Pair with Vehicle")
                // VIN gate: pairing without a VIN can never succeed (the
                // session needs it for the BLE handshake).
                enabled: page.configReady && !page.generating && !page.pairing && page.pendingListKeysId.length === 0 && page.publicKeyPem.length > 0 && page.hasVin
                onClicked: {
                    page.pairing = true
                    page.pairStatus = qsTr("Requesting pairing over BLE - approve on the car's touchscreen / NFC card now.")
                    teslaClient.pair()
                }
            }

            BusyIndicator {
                anchors.horizontalCenter: parent.horizontalCenter
                running: page.pairing
                visible: running
            }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.Wrap
                text: page.pairStatus
                font.pixelSize: Theme.fontSizeExtraSmall
            }

            SectionHeader { text: qsTr("Enrolled Keys") }

            Button {
                anchors.horizontalCenter: parent.horizontalCenter
                text: qsTr("List Enrolled Keys")
                enabled: page.configReady && page.hasVin && !page.generating && !page.pairing && page.pendingListKeysId.length === 0
                onClicked: {
                    if (page.pendingListKeysId.length > 0)
                        return
                    page.keysListOutput = ""
                    page.pendingListKeysId = teslaClient.newRequestId("list-keys", page.vin)
                    teslaClient.runCommand(page.pendingListKeysId, "list-keys", [])
                }
            }

            BusyIndicator {
                anchors.horizontalCenter: parent.horizontalCenter
                running: page.pendingListKeysId.length > 0
                visible: running
            }

            Label {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.margins: Theme.horizontalPageMargin
                wrapMode: Text.WrapAnywhere
                font.pixelSize: Theme.fontSizeExtraSmall
                font.family: "monospace"
                text: page.keysListOutput
            }
        }
    }
}
