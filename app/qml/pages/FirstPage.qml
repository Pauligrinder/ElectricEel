import QtQuick 2.6
import Sailfish.Silica 1.0
import "../js/CommandCatalog.js" as Catalog

Page {
    id: page
    property var teslaClient

    property string vin: ""
    property bool hasKey: false

    function refresh() {
        teslaClient.refreshHelperAvailable()
        teslaClient.refreshConfig()
    }

    Connections {
        target: teslaClient
        onConfigLoaded: {
            page.vin = vin
            page.hasKey = hasKey
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

            SectionHeader { text: "Categories" }
        }

        delegate: BackgroundItem {
            width: listView.width
            height: Theme.itemSizeMedium

            onClicked: pageStack.push(Qt.resolvedUrl("CategoryPage.qml"), {
                teslaClient: teslaClient,
                categoryId: modelData.id,
                categoryTitle: modelData.title
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
        }

        VerticalScrollDecorator {}
    }
}
