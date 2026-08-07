import QtQuick 2.6
import Sailfish.Silica 1.0
import harbour.teslacontrol 1.0

ApplicationWindow
{
    id: appWindow

    TeslaClient {
        id: teslaClient
    }

    initialPage: Component {
        FirstPage {
            teslaClient: teslaClient
        }
    }
    cover: Qt.resolvedUrl("cover/CoverPage.qml")
    allowedOrientations: Orientation.All
}
