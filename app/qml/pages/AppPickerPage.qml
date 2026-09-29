import QtQuick 2.6
import Sailfish.Silica 1.0

Page {
    id: page
    property var teslaClient

    ListModel { id: appsModel }

    Component.onCompleted: {
        var list = teslaClient.installedApps()
        for (var i = 0; i < list.length; ++i)
            appsModel.append(list[i])
    }

    SilicaListView {
        id: listView
        anchors.fill: parent
        model: appsModel
        currentIndex: -1

        header: PageHeader {
            title: qsTr("Choose app")
        }

        delegate: ListItem {
            id: item
            width: listView.width
            contentHeight: Theme.itemSizeMedium

            property bool selected: model.desktopFile === teslaClient.driveAppDesktopFile

            Row {
                anchors.fill: parent
                anchors.leftMargin: Theme.horizontalPageMargin
                anchors.rightMargin: Theme.horizontalPageMargin
                spacing: Theme.paddingMedium

                Image {
                    id: icon
                    anchors.verticalCenter: parent.verticalCenter
                    width: Theme.iconSizeMedium
                    height: Theme.iconSizeMedium
                    fillMode: Image.PreserveAspectFit
                    source: {
                        var iconName = model.icon || ""
                        if (iconName.indexOf("/") === 0 || iconName.indexOf("file:") === 0)
                            return iconName
                        if (iconName.length > 0)
                            return "image://theme/" + iconName
                        return "image://theme/icon-m-application"
                    }
                    onStatusChanged: {
                        if (status === Image.Error)
                            source = "image://theme/icon-m-application"
                    }
                }

                Label {
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width - icon.width - Theme.paddingMedium
                    text: model.name
                    color: item.highlighted || item.selected
                           ? Theme.highlightColor : Theme.primaryColor
                    truncationMode: TruncationMode.Fade
                }
            }

            onClicked: {
                teslaClient.driveAppDesktopFile = model.desktopFile
                pageStack.pop()
            }
        }

        ViewPlaceholder {
            enabled: listView.count === 0
            text: qsTr("No apps found")
            hintText: qsTr("Install an app from the Store or Android support")
        }

        VerticalScrollDecorator {}
    }
}
