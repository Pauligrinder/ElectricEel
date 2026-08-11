import QtQuick 2.6
import Sailfish.Silica 1.0
import "../js/CommandCatalog.js" as Catalog
import "../js/VehicleState.js" as VState

Page {
    id: page
    property var teslaClient

    property string vin: ""
    property bool hasKey: false

    // Live dashboard state, fetched over BLE via three chained "state"
    // subcommand calls (see refreshStatus() - chained rather than parallel
    // because each is its own BLE connect+handshake and hammering the
    // adapter with three concurrent ones is a worse idea than a ~1s longer
    // sequential refresh). statusStage tracks which leg is in flight so the
    // UI can show a single busy indicator and refuse to stack requests.
    property var status: VState.emptyStatus()
    property string statusStage: ""
    // Set from stdErr when a status leg fails. All three legs go through
    // the Infotainment domain (unlike lock/unlock and body-controller-state,
    // which use VCSEC and work even while the car sleeps - see
    // VehicleState.js), so a sleeping vehicle is the expected way for this
    // to fail even with everything else configured correctly.
    property string statusError: ""

    function refresh() {
        teslaClient.refreshHelperAvailable()
        teslaClient.refreshConfig()
    }

    function refreshStatus() {
        if (!teslaClient.helperAvailable || !page.hasKey || page.vin.length === 0 || page.statusStage.length > 0)
            return
        page.statusError = ""
        page.statusStage = "closures"
        teslaClient.runCommand("status:closures", "state", ["closures"])
    }

    function toggleLock() {
        if (page.statusStage.length > 0)
            return
        page.statusStage = "toggle"
        teslaClient.runCommand("status:toggle", page.status.locked ? "unlock" : "lock", [])
    }

    function toggleClimate() {
        if (page.statusStage.length > 0)
            return
        page.statusStage = "toggle"
        teslaClient.runCommand("status:toggle", page.status.isClimateOn ? "climate-off" : "climate-on", [])
    }

    Connections {
        target: teslaClient
        onConfigLoaded: {
            page.vin = vin
            page.hasKey = hasKey
            page.refreshStatus()
        }
    }

    Connections {
        target: teslaClient
        onCommandFinished: {
            if (requestId === "status:closures") {
                if (ok)
                    page.status = VState.mergeClosuresState(page.status, stdOut)
                else
                    page.statusError = stdErr.length ? stdErr : ("exit code " + exitCode)
                page.statusStage = "climate"
                teslaClient.runCommand("status:climate", "state", ["climate"])
            } else if (requestId === "status:climate") {
                if (ok)
                    page.status = VState.mergeClimateState(page.status, stdOut)
                else if (page.statusError.length === 0)
                    page.statusError = stdErr.length ? stdErr : ("exit code " + exitCode)
                page.statusStage = "charge"
                teslaClient.runCommand("status:charge", "state", ["charge"])
            } else if (requestId === "status:charge") {
                if (ok)
                    page.status = VState.mergeChargeState(page.status, stdOut)
                else if (page.statusError.length === 0)
                    page.statusError = stdErr.length ? stdErr : ("exit code " + exitCode)
                page.statusStage = ""
            } else if (requestId === "status:toggle") {
                // Whether the lock/climate toggle succeeded or not, re-fetch
                // so the dashboard reflects reality rather than an assumed
                // new state (the command can "succeed" over BLE without the
                // vehicle confirming - see protocol.MayHaveSucceeded usage
                // elsewhere in this app).
                page.statusStage = ""
                page.refreshStatus()
            }
        }
        onCommandError: {
            if (requestId.indexOf("status:") === 0) {
                page.statusStage = ""
                if (page.statusError.length === 0)
                    page.statusError = message
            }
        }
    }

    Component.onCompleted: refresh()

    SilicaListView {
        id: listView
        anchors.fill: parent
        model: Catalog.CATEGORIES

        header: Column {
            width: listView.width

            PageHeader {
                title: "Tesla Control"
            }

            Rectangle {
                width: parent.width
                height: helperWarning.visible ? helperWarning.height + Theme.paddingMedium * 2 : 0
                color: Theme.rgba(Theme.highlightBackgroundColor, 0.15)
                visible: !teslaClient.helperAvailable

                Column {
                    id: helperWarning
                    anchors.centerIn: parent
                    width: parent.width - Theme.horizontalPageMargin * 2
                    visible: !teslaClient.helperAvailable
                    spacing: Theme.paddingSmall

                    Label {
                        width: parent.width
                        wrapMode: Text.Wrap
                        color: Theme.highlightColor
                        font.pixelSize: Theme.fontSizeSmall
                        text: "teslacontrold helper service not found. Install the companion package (devel-su pkcon install-local teslacontrold-*.rpm) and pull down to refresh."
                    }
                }
            }

            BackgroundItem {
                width: parent.width
                height: Theme.itemSizeMedium
                onClicked: pageStack.push(Qt.resolvedUrl("SettingsPage.qml"), { teslaClient: teslaClient })

                Column {
                    anchors.verticalCenter: parent.verticalCenter
                    anchors.left: parent.left
                    anchors.leftMargin: Theme.horizontalPageMargin
                    anchors.right: parent.right
                    anchors.rightMargin: Theme.horizontalPageMargin

                    Label {
                        text: page.vin.length > 0 ? page.vin : "No VIN configured"
                        color: page.vin.length > 0 ? Theme.primaryColor : Theme.secondaryColor
                    }
                    Label {
                        text: page.hasKey ? "Key ready" : "No key - tap for Settings / Pairing"
                        font.pixelSize: Theme.fontSizeExtraSmall
                        color: page.hasKey ? Theme.secondaryHighlightColor : Theme.secondaryColor
                    }
                }
            }

            Rectangle {
                width: parent.width
                height: statusColumn.height + Theme.paddingMedium * 2
                visible: page.hasKey && page.vin.length > 0
                color: Theme.rgba(Theme.highlightBackgroundColor, 0.08)

                Column {
                    id: statusColumn
                    width: parent.width - Theme.horizontalPageMargin * 2
                    anchors.horizontalCenter: parent.horizontalCenter
                    anchors.verticalCenter: parent.verticalCenter
                    spacing: Theme.paddingSmall

                    Row {
                        width: parent.width

                        BackgroundItem {
                            width: parent.width / 3
                            height: Theme.itemSizeSmall
                            onClicked: page.toggleLock()

                            Label {
                                anchors.centerIn: parent
                                font.pixelSize: Theme.fontSizeSmall
                                color: page.status.locked ? Theme.secondaryHighlightColor : Theme.highlightColor
                                text: page.status.locked === null ? "Lock: ?" : (page.status.locked ? "Locked" : "Unlocked")
                            }
                        }

                        Item {
                            width: parent.width / 3
                            height: Theme.itemSizeSmall

                            Label {
                                anchors.centerIn: parent
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.primaryColor
                                // "Charging" (PascalCase) is correct here - it's the
                                // oneof variant name VehicleState.js's oneofVariantName()
                                // extracts, verified against real protojson output, not a
                                // guess. See VehicleState.js's comment for why this isn't
                                // "CHARGING" or a plain enum string.
                                text: page.status.batteryLevel === null ? "Battery: ?" : (page.status.batteryLevel + "%" +
                                      (page.status.chargingState === "Charging" ? " (charging)" : ""))
                            }
                        }

                        BackgroundItem {
                            width: parent.width / 3
                            height: Theme.itemSizeSmall
                            onClicked: page.toggleClimate()

                            Label {
                                anchors.centerIn: parent
                                font.pixelSize: Theme.fontSizeSmall
                                color: page.status.isClimateOn ? Theme.secondaryHighlightColor : Theme.highlightColor
                                text: page.status.insideTemp === null ? "Climate: ?" : (page.status.insideTemp.toFixed(0) + "°C")
                            }
                        }
                    }

                    Row {
                        anchors.horizontalCenter: parent.horizontalCenter
                        spacing: Theme.paddingSmall

                        BusyIndicator {
                            size: BusyIndicatorSize.ExtraSmall
                            running: page.statusStage.length > 0
                            visible: running
                        }
                        Label {
                            width: statusColumn.width
                            horizontalAlignment: Text.AlignHCenter
                            wrapMode: Text.Wrap
                            font.pixelSize: Theme.fontSizeTiny
                            color: page.statusError.length > 0 && page.statusStage.length === 0 ? Theme.highlightColor : Theme.secondaryColor
                            text: {
                                if (page.statusStage.length > 0)
                                    return "Updating..."
                                if (page.statusError.length > 0 && VState.minutesAgo(page.status.updatedAt) < 0)
                                    return "Status unavailable (" + page.statusError + "). Vehicle may be asleep - try Wake Vehicle, then Refresh Status."
                                if (VState.minutesAgo(page.status.updatedAt) < 0)
                                    return "Tap a status to refresh"
                                if (VState.minutesAgo(page.status.updatedAt) === 0)
                                    return "Updated just now"
                                return "Updated " + VState.minutesAgo(page.status.updatedAt) + "m ago"
                            }
                        }
                    }
                }
            }

            SectionHeader { text: "Categories" }
        }

        delegate: BackgroundItem {
            width: listView.width
            height: Theme.itemSizeMedium

            onClicked: pageStack.push(Qt.resolvedUrl("CategoryPage.qml"), {
                teslaClient: teslaClient,
                categoryId: modelData.id,
                categoryTitle: modelData.title,
                status: page.status
            })

            Label {
                anchors.verticalCenter: parent.verticalCenter
                anchors.left: parent.left
                anchors.leftMargin: Theme.horizontalPageMargin
                text: modelData.title
            }
        }

        PullDownMenu {
            MenuItem {
                text: "Pair Vehicle"
                onClicked: pageStack.push(Qt.resolvedUrl("PairingPage.qml"), { teslaClient: teslaClient })
            }
            MenuItem {
                text: "Settings"
                onClicked: pageStack.push(Qt.resolvedUrl("SettingsPage.qml"), { teslaClient: teslaClient })
            }
            MenuItem {
                text: "Refresh"
                onClicked: page.refresh()
            }
            MenuItem {
                text: "Refresh Status"
                visible: page.hasKey && page.vin.length > 0
                onClicked: page.refreshStatus()
            }
        }

        VerticalScrollDecorator {}
    }
}
