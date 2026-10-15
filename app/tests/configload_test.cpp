#include "../src/teslaclient.h"
#include <QGuiApplication>
#include <QJsonDocument>
#include <cassert>

extern "C" {
#include "runtime_stub/electriceelcore.h"

static bool acceptRequests = false;
static QJsonObject submitted;
bool runtime_submit(Runtime *, const char *json) {
    submitted = QJsonDocument::fromJson(json).object();
    return acceptRequests;
}
void runtime_observe(Runtime *, void (*)(void *, const char *), void *) {}
const char *core_version() { return "test"; }
}

int main(int argc, char **argv)
{
    qputenv("QT_QPA_PLATFORM", "minimal");
    QGuiApplication app(argc, argv);
    TeslaClient client(nullptr);
    int loaded = 0;
    int failures = 0;
    QObject::connect(&client, &TeslaClient::configLoaded, [&] { ++loaded; });
    QObject::connect(&client, &TeslaClient::configLoadError, [&](const QString &message) {
        assert(!message.isEmpty());
        ++failures;
    });
    client.refreshConfig();
    assert(loaded == 0);
    assert(failures == 1);

    int exports = 0;
    QObject::connect(&client, &TeslaClient::automagicExportFinished,
                     [&](bool ok, const QString &path, const QString &message) {
        assert(!ok);
        assert(path.isEmpty());
        assert(!message.isEmpty());
        assert(!client.automagicExportBusy());
        ++exports;
    });
    client.exportAutomagicFlows();
    assert(exports == 1); // Rejected admission must stop the UI busy state.

    acceptRequests = true;
    client.setConfig("", "", "phone", 20, 5, "flash-lights", "honk");
    assert(submitted.value("op").toString() == "set_config");
    assert(submitted.value("cover_left_action").toString() == "flash-lights");
    assert(submitted.value("cover_right_action").toString() == "honk");

    QString leftAction, rightAction;
    QObject::connect(&client, &TeslaClient::configLoaded,
                     [&](const QString &, const QString &, const QString &, int, int,
                         bool, const QString &, const QString &left, const QString &right) {
        leftAction = left;
        rightAction = right;
    });
    const QByteArray notification = R"({"type":"config_loaded","cover_left_action":"flash-lights","cover_right_action":"honk"})";
    const bool delivered = QMetaObject::invokeMethod(&client, "deliverNotification",
        Qt::DirectConnection, Q_ARG(QByteArray, notification));
    assert(delivered);
    assert(loaded == 1);
    assert(leftAction == "flash-lights");
    assert(rightAction == "honk");
}
