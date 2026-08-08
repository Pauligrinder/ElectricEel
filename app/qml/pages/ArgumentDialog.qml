import QtQuick 2.6
import Sailfish.Silica 1.0

// Builds an input form from a CommandCatalog command's `args` array and,
// on accept, fills `values` with one string per arg (in order) ready to
// pass straight to TeslaClient.runCommand(). One dialog handles every
// tesla-control subcommand's argument shape instead of one per command.
Dialog {
    id: dialog

    property var commandDef
    property var values: []
    property bool formValid: false

    // Recompute formValid. Called from each field's value handlers and from
    // Component.onCompleted. It cannot be a plain binding expression because
    // QML does not track plain JS object properties (argSpec.__value), so a
    // canAccept binding would be evaluated once and never again, leaving the
    // Run button permanently disabled.
    function revalidate() {
        if (!commandDef) {
            formValid = false
            return
        }
        for (var i = 0; i < commandDef.args.length; i++) {
            var a = commandDef.args[i]
            if (!a.optional && (!a.__value || a.__value.length === 0)) {
                formValid = false
                return
            }
        }
        formValid = true
    }

    canAccept: dialog.formValid

    Component.onCompleted: dialog.revalidate()

    DialogHeader {
        title: commandDef ? commandDef.label : ""
        acceptText: "Run"
    }

    SilicaFlickable {
        anchors.fill: parent
        contentHeight: column.height + Theme.paddingLarge

        Column {
            id: column
            width: parent.width
            spacing: Theme.paddingMedium
            anchors.top: parent.top
            anchors.topMargin: Theme.itemSizeLarge + Theme.paddingMedium

            Repeater {
                model: commandDef ? commandDef.args : []

                delegate: Loader {
                    width: column.width
                    property var argSpec: modelData
                    sourceComponent: {
                        if (modelData.type === "enum") return enumField
                        if (modelData.type === "pin") return pinField
                        if ((modelData.type === "int" || modelData.type === "float")
                                && modelData.min !== undefined && modelData.max !== undefined)
                            return sliderField
                        return textField
                    }
                    onLoaded: item.argSpec = argSpec
                }
            }
        }
    }

    Component {
        id: enumField
        ComboBox {
            property var argSpec
            label: argSpec ? (argSpec.name + (argSpec.optional ? " (optional)" : "")) : ""
            menu: ContextMenu {
                Repeater {
                    model: argSpec ? argSpec.values : []
                    MenuItem { text: modelData }
                }
            }
            onCurrentIndexChanged: {
                if (argSpec && argSpec.values) argSpec.__value = argSpec.values[currentIndex]
                dialog.revalidate()
            }
            Component.onCompleted: {
                if (argSpec && argSpec.values && argSpec.values.length) argSpec.__value = argSpec.values[0]
                dialog.revalidate()
            }
        }
    }

    Component {
        id: pinField
        TextField {
            property var argSpec
            label: argSpec ? argSpec.name : ""
            echoMode: TextInput.Password
            inputMethodHints: Qt.ImhDigitsOnly
            onTextChanged: {
                if (argSpec) argSpec.__value = text
                dialog.revalidate()
            }
        }
    }

    Component {
        id: sliderField
        Slider {
            property var argSpec
            label: argSpec ? (argSpec.name + (argSpec.unit ? (" (" + argSpec.unit + ")") : "")) : ""
            minimumValue: argSpec ? argSpec.min : 0
            maximumValue: argSpec ? argSpec.max : 100
            stepSize: argSpec && argSpec.step ? argSpec.step : 1
            value: argSpec && argSpec.def !== undefined ? argSpec.def : minimumValue
            valueText: value.toFixed(argSpec && argSpec.step && argSpec.step < 1 ? 1 : 0)
            onValueChanged: {
                if (argSpec) argSpec.__value = value.toString()
                dialog.revalidate()
            }
            Component.onCompleted: {
                if (argSpec) argSpec.__value = value.toString()
                dialog.revalidate()
            }
        }
    }

    Component {
        id: textField
        TextField {
            property var argSpec
            label: argSpec ? (argSpec.name + (argSpec.optional ? " (optional)" : "")) : ""
            placeholderText: argSpec && argSpec.placeholder ? argSpec.placeholder : ""
            text: argSpec && argSpec.def !== undefined ? String(argSpec.def) : ""
            onTextChanged: {
                if (argSpec) argSpec.__value = text
                dialog.revalidate()
            }
            Component.onCompleted: {
                if (argSpec) argSpec.__value = text
                dialog.revalidate()
            }
        }
    }

    onAccepted: {
        var out = []
        for (var i = 0; i < commandDef.args.length; i++) {
            var a = commandDef.args[i]
            var v = a.__value !== undefined ? a.__value : ""
            if (v === "" && a.optional)
                continue
            out.push(v)
        }
        values = out
    }
}
