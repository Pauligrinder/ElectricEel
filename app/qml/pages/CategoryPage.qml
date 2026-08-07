import QtQuick 2.6
import Sailfish.Silica 1.0
import "../js/CommandCatalog.js" as Catalog

Page {
    id: page
    property var teslaClient
    property string categoryId
    property string categoryTitle

    property var category: Catalog.findCategory(categoryId)
    property string pendingCmd: ""
    property string lastResult: ""
    property bool lastOk: true

    Connections {
        target: teslaClient
        onCommandFinished: {
            if (requestId !== page.pendingCmd)
                return
            page.pendingCmd = ""
            page.lastOk = ok
            page.lastResult = ok ? (stdOut.length ? stdOut : "OK") : (stdErr.length ? stdErr : ("exit code " + exitCode))
        }
        onCommandError: {
            if (requestId !== page.pendingCmd)
                return
            page.pendingCmd = ""
            page.lastOk = false
            page.lastResult = message
        }
    }

    function execute(commandDef, argValues) {
        page.pendingCmd = commandDef.id
        page.lastResult = ""
        teslaClient.runCommand(commandDef.id, commandDef.id, argValues || [])
    }

    function openArgs(commandDef) {
        var dialog = pageStack.push(Qt.resolvedUrl("ArgumentDialog.qml"), { commandDef: commandDef })
        dialog.accepted.connect(function() {
            execute(commandDef, dialog.values)
        })
    }

    SilicaListView {
        id: listView
        anchors.fill: parent
        model: category ? category.commands : []

        header: Column {
            width: listView.width

            PageHeader { title: categoryTitle }

            Rectangle {
                width: parent.width
                height: resultLabel.height + Theme.paddingMedium * 2
                visible: page.pendingCmd.length > 0 || page.lastResult.length > 0
                color: Theme.rgba(page.lastOk ? Theme.secondaryHighlightColor : Theme.highlightBackgroundColor, 0.15)

                Row {
                    id: resultLabel
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.margins: Theme.horizontalPageMargin
                    anchors.verticalCenter: parent.verticalCenter
                    spacing: Theme.paddingSmall

                    BusyIndicator {
                        size: BusyIndicatorSize.Small
                        running: page.pendingCmd.length > 0
                        visible: running
                    }
                    Label {
                        width: parent.width - (page.pendingCmd.length > 0 ? Theme.itemSizeSmall : 0)
                        wrapMode: Text.Wrap
                        font.pixelSize: Theme.fontSizeExtraSmall
                        color: page.lastOk ? Theme.primaryColor : Theme.highlightColor
                        text: page.pendingCmd.length > 0 ? ("Running " + page.pendingCmd + "...") : page.lastResult
                    }
                }
            }
        }

        delegate: BackgroundItem {
            width: listView.width
            height: Theme.itemSizeMedium
            enabled: page.pendingCmd.length === 0

            onClicked: {
                if (modelData.args && modelData.args.length > 0)
                    page.openArgs(modelData)
                else
                    page.execute(modelData, [])
            }

            Label {
                anchors.verticalCenter: parent.verticalCenter
                anchors.left: parent.left
                anchors.leftMargin: Theme.horizontalPageMargin
                anchors.right: parent.right
                anchors.rightMargin: Theme.horizontalPageMargin
                text: modelData.label
                truncationMode: TruncationMode.Fade
            }
        }

        VerticalScrollDecorator {}
    }
}
