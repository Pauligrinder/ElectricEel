#include <sailfishapp.h>
#include <QGuiApplication>
#include <QQuickView>
#include <QtQml>

#include "teslaclient.h"

int main(int argc, char *argv[])
{
    QGuiApplication *app = SailfishApp::application(argc, argv);
    QQuickView *view = SailfishApp::createView();

    qmlRegisterType<TeslaClient>("harbour.electriceel", 1, 0, "TeslaClient");

    view->setSource(SailfishApp::pathTo(QStringLiteral("qml/harbour-electric-eel.qml")));
    view->show();

    return app->exec();
}
