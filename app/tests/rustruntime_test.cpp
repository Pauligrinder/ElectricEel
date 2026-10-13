#include "../src/teslaclient.h"
#include <QGuiApplication>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QThread>
#include <QtTest>
#include <QSemaphore>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QStandardPaths>
#include <dlfcn.h>
#include "tesla_ble.h"

extern "C" {
#include "electriceelcore.h"
}

// Link the actual Rust entrypoint and runtime to the actual Qt adapter, replacing
// only the Sailfish view callbacks with this headless test UI.
int main(int argc, char *argv[])
{
    return electric_eel_app_main(argc, argv);
}

static QGuiApplication *application = nullptr;
static Runtime *runtime = nullptr;
static QTemporaryDir directory;
static QByteArray stateDirectory;

class RustRuntimeTest : public QObject
{
    Q_OBJECT
private slots:
    void goCallbacksAndRuntimeCoexistWithQt() {
        void *library = dlopen(TESLA_BLE_LIBRARY, RTLD_NOW | RTLD_LOCAL);
        QVERIFY2(library, dlerror());
        auto version = reinterpret_cast<decltype(&ee_abi_version)>(dlsym(library, "ee_abi_version"));
        auto create = reinterpret_cast<decltype(&ee_session_create)>(dlsym(library, "ee_session_create"));
        auto submit = reinterpret_cast<decltype(&ee_session_submit)>(dlsym(library, "ee_session_submit"));
        auto stop = reinterpret_cast<decltype(&ee_session_stop)>(dlsym(library, "ee_session_stop"));
        auto destroy = reinterpret_cast<decltype(&ee_session_destroy)>(dlsym(library, "ee_session_destroy"));
        QVERIFY(version && create && submit && stop && destroy);
        QCOMPARE(version(), uint32_t(2));
        struct CallbackState { QSemaphore ready; uint32_t status = EE_INTERNAL_BUG; QByteArray payload; } state;
        uint8_t scalar[32] = {}; scalar[31] = 1;
        const QByteArray vin("5YJ3E1EA7KF000001");
        ee_config config = {};
        config.size = sizeof(config); config.version = 2;
        config.vin = {reinterpret_cast<const uint8_t *>(vin.constData()), uint64_t(vin.size())};
        config.private_scalar = {scalar, sizeof(scalar)};
        uint64_t handle = 0;
        QCOMPARE(create(&config, [](void *context, const ee_message *message) {
            auto *state = static_cast<CallbackState *>(context);
            if (message->kind != EE_COMPLETE) return;
            state->status = message->status;
            state->payload = QByteArray(reinterpret_cast<const char *>(message->payload.data), int(message->payload.len));
            state->ready.release();
        }, &state, &handle), uint32_t(EE_OK));
        ee_request request = {};
        request.size = sizeof(request); request.operation = EE_PUBLIC_KEY;
        request.id = 1; request.timeout_ms = 1000;
        QCOMPARE(submit(handle, &request), uint32_t(EE_OK));
        QVERIFY(state.ready.tryAcquire(1, 10000));
        QCOMPARE(state.status, uint32_t(EE_OK));
        QCOMPARE(state.payload.size(), 65);
        QCOMPARE(stop(handle), uint32_t(EE_OK));
        QCOMPARE(destroy(handle), uint32_t(EE_OK));
        // Keep the Go runtime loaded until process exit, like the Rust loader.
    }

    void rustPushesResultsToTheGuiThread() {
        TeslaClient client(runtime);
        QSignalSpy configs(&client, &TeslaClient::configLoaded);
        client.refreshConfig();
        QTRY_COMPARE(configs.count(), 1);
        QCOMPARE(configs.first().at(3).toInt(), 20);
        QVERIFY(!configs.first().at(5).toBool());
        QTRY_VERIFY(!client.phoneKeyStatus().isEmpty());
        QVERIFY(!client.phoneKeyActive());
        QTRY_COMPARE(client.phoneKeyLink(), QStringLiteral("unpaired"));

        QSignalSpy errors(&client, &TeslaClient::commandError);
        const QString id = QString::fromUtf8("quoted-\"-città");
        client.runCommand(id, "not-a-command", {});
        QTRY_COMPARE(errors.count(), 1);
        QCOMPARE(errors.first().at(0).toString(), id);

        QSignalSpy previews(&client, &TeslaClient::destinationPreviewed);
        client.previewDestination("navigation", "geo:45,9");
        QTRY_COMPARE(previews.count(), 1);
        QVERIFY(previews.first().at(1).toBool());
        QCOMPARE(previews.first().at(2).toString(), QString("gps"));

        QSignalSpy refreshes(&client, &TeslaClient::statusRefreshRequested);
        bool guiThread = false;
        connect(&client, &TeslaClient::statusRefreshRequested, this, [&guiThread] {
            guiThread = QThread::currentThread() == qApp->thread();
        });
        client.runCommand("toggle-confirmation", "not-a-command", {}, true);
        QThread::msleep(2800); // Rust's deadline expires without GUI processing.
        QCOMPARE(refreshes.count(), 0); // Qt delivery must remain queued.
        QTRY_COMPARE(refreshes.count(), 1);
        QVERIFY(guiThread);
    } // Detaches callback before the Rust entrypoint destroys its runtime.

    void phoneKeyPropertiesAreCoherentAndProseIsDisplayOnly() {
        TeslaClient client(runtime);
        bool coherent = false;
        connect(&client, &TeslaClient::phoneKeyActiveChanged, this, [&] {
            coherent = client.phoneKeyActive()
                && client.phoneKeyLink() == QStringLiteral("authorized")
                && client.phoneKeyStatus() == QStringLiteral("arbitrary diagnostic");
        });
        QVERIFY(QMetaObject::invokeMethod(&client, "deliverNotification", Qt::DirectConnection,
            Q_ARG(QByteArray, QByteArray(R"({"type":"phone_key_state","active":true,"link":"authorized","status":"arbitrary diagnostic"})"))));
        QVERIFY(coherent);
        QVERIFY(QMetaObject::invokeMethod(&client, "deliverNotification", Qt::DirectConnection,
            Q_ARG(QByteArray, QByteArray(R"({"type":"phone_key_state","active":true,"link":"future-value","status":"Phone key connected"})"))));
        QCOMPARE(client.phoneKeyLink(), QStringLiteral("error"));
        QCOMPARE(client.phoneKeyStatus(), QStringLiteral("Phone key connected"));
    }

    void automagicExportCompletesOnGuiThreadAndDoubleClickIsSingleFlight() {
        const QString documents = QStandardPaths::writableLocation(QStandardPaths::DocumentsLocation);
        QVERIFY(documents.startsWith(directory.path() + QLatin1Char('/')));
        TeslaClient client(runtime);
        QSignalSpy exports(&client, &TeslaClient::automagicExportFinished);
        bool guiThread = false;
        connect(&client, &TeslaClient::automagicExportFinished, this, [&] {
            guiThread = QThread::currentThread() == qApp->thread();
        });
        client.exportAutomagicFlows();
        QVERIFY(client.automagicExportBusy());
        client.exportAutomagicFlows();
        QCOMPARE(exports.count(), 0); // Completion is queued, never synchronous.
        QTRY_COMPARE(exports.count(), 1);
        QVERIFY(exports.first().at(0).toBool());
        QVERIFY(exports.first().at(2).toString().isEmpty());
        QVERIFY(!client.automagicExportBusy());
        QVERIFY(guiThread);
        const QString path = exports.first().at(1).toString();
        QCOMPARE(QFileInfo(path).absolutePath(), documents);
        for (const QString &file : {QString("data_sources.json"), QString("actions.json"),
                                   QString("flows.json"), QString("value_maps.json")}) {
            QVERIFY(QFile::exists(path + QLatin1Char('/') + file));
        }
        QCOMPARE(QDir(documents).entryList(QDir::Dirs | QDir::NoDotAndDotDot).size(), 1);
    }
};

extern "C" const char *electric_eel_ui_prepare(int argc, char **argv)
{
    static int qtArgc;
    qtArgc = argc;
    qputenv("QT_QPA_PLATFORM", "minimal");
    application = new QGuiApplication(qtArgc, argv);
    if (!directory.isValid()) return nullptr;
    stateDirectory = directory.path().toUtf8();
    qputenv("HOME", stateDirectory); // Documents exports stay inside the test fixture.
    qputenv("XDG_CONFIG_HOME", stateDirectory);
    qputenv("ELECTRIC_EEL_LOG_DIR", stateDirectory);
    return stateDirectory.constData();
}

extern "C" int electric_eel_ui_run(Runtime *handle)
{
    runtime = handle;
    RustRuntimeTest test;
    return QTest::qExec(&test);
}

extern "C" void electric_eel_ui_cleanup()
{
    delete application;
    application = nullptr;
}

#include "rustruntime_test.moc"
