import QtQuick 2.6
import Sailfish.Silica 1.0

// App and core are stamped from the same release tag and linked together;
// a difference means a broken build or install.
Label {
    property var client
    readonly property bool mismatch: !!client && client.helperVersion !== client.appVersion
    x: Theme.horizontalPageMargin
    width: (parent ? parent.width : 0) - Theme.horizontalPageMargin * 2
    height: mismatch ? implicitHeight + Theme.paddingMedium : 0
    visible: mismatch
    wrapMode: Text.Wrap
    color: Theme.highlightColor
    font.pixelSize: Theme.fontSizeExtraSmall
    text: mismatch ? qsTr("Version mismatch: core %1 vs app %2 - reinstall the app.")
                     .arg(client.helperVersion).arg(client.appVersion) : ""
}
