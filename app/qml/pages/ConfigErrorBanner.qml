import QtQuick 2.6
import Sailfish.Silica 1.0

// Shared configuration-load failure notice with a retry, shown by every
// page that depends on TeslaClient's configuration properties.
Column {
    id: banner
    property var client
    width: parent ? parent.width : 0
    visible: !!client && client.configError.length > 0
    spacing: Theme.paddingSmall

    Label {
        x: Theme.horizontalPageMargin
        width: parent.width - Theme.horizontalPageMargin * 2
        wrapMode: Text.Wrap
        text: banner.client ? banner.client.configError : ""
        color: Theme.highlightColor
    }
    Button {
        anchors.horizontalCenter: parent.horizontalCenter
        text: qsTr("Retry")
        onClicked: banner.client.refreshConfig()
    }
}
