#include "teslaclient.h"

#include <QDebug>
#include <QStandardPaths>
#include <QThread>
#include <QDir>

// The cbindgen-generated C header for the in-process Rust core
// (helper/electriceelcore.h). Wrapped in extern "C" because cbindgen emits a
// plain C header with no include guard of its own linkage.
extern "C" {
#include "electriceelcore.h"
}

namespace {

// Binaries the core spawns live under the app's data dir in the RPM. The Go
// tesla-session is bundled there by the spec; tesla-control/tesla-keygen were
// the pre-in-process one-shot fallbacks and no longer exist in this design.
const char *kBinDir = "/usr/share/harbour-electric-eel/bin";
const char *kSessionBin = "/usr/share/harbour-electric-eel/bin/tesla-session";
// BlueZ is the cooperative transport the app uses by default (see
// BLUEZ_BACKEND_PLAN.md); "hci" raw-HCI is an escape hatch, not what ships.
const char *kBleBackend = "bluez";

// Converts a Rust-owned C string from an output slot into a QString and frees
// it. A NULL slot (never written by the ABI) yields an empty QString.
QString takeCString(char *ptr)
{
    if (!ptr)
        return QString();
    const QString value = QString::fromUtf8(ptr);
    core_string_free(ptr);
    return value;
}

} // namespace

CoreWorker::CoreWorker(QObject *parent)
    : QObject(parent)
    , m_core(nullptr)
{
}

CoreWorker::~CoreWorker()
{
    if (m_core) {
        // Runs on the GUI thread (the worker is deleted there after its
        // thread stopped), which is safe: nothing touches m_core anymore.
        core_free(m_core);
        m_core = nullptr;
    }
}

void CoreWorker::initialize(const QString &binDir, const QString &stateDir, const QString &sessionBin)
{
    const QByteArray binDirBa = binDir.toUtf8();
    const QByteArray stateDirBa = stateDir.toUtf8();
    const QByteArray sessionBa = sessionBin.toUtf8();
    const QByteArray backendBa = QByteArray(kBleBackend);

    char *err = nullptr;
    // core_new reads config.json only (missing => defaults) and spawns
    // nothing; the Go session child is launched lazily on first run().
    m_core = core_new(binDirBa.constData(), stateDirBa.constData(),
                      sessionBa.constData(), backendBa.constData(), &err);
    if (!m_core) {
        const QString message = takeCString(err);
        emit initialized(false, message.isEmpty()
                         ? QStringLiteral("core_new failed: unknown error")
                         : message);
        return;
    }
    emit initialized(true, QString());
}

void CoreWorker::runCommand(const QString &requestId, const QString &cmd, const QStringList &args)
{
    if (!m_core) {
        emit commandError(requestId, QStringLiteral("control core not initialized"));
        return;
    }

    // Build a NULL-terminated argv array. QStringList -> UTF-8 buffers that
    // outlive the call; the final NULL element terminates the array.
    QByteArray cmdBa = cmd.toUtf8();
    QList<QByteArray> argBas;
    argBas.reserve(args.size());
    for (const QString &arg : args)
        argBas.append(arg.toUtf8());

    QVector<const char *> argv;
    argv.reserve(argBas.size() + 1);
    for (const QByteArray &ba : argBas)
        argv.append(ba.constData());
    argv.append(nullptr);

    bool ok = false;
    char *outStdout = nullptr;
    char *outStderr = nullptr;
    int32_t exitCode = 0;
    char *errorMessage = nullptr;
    const CoreError rc = core_run(m_core, cmdBa.constData(), argv.constData(),
                                  &ok, &outStdout, &outStderr, &exitCode, &errorMessage);

    if (rc != CoreError::Ok) {
        const QString message = takeCString(errorMessage);
        emit commandError(requestId, message.isEmpty()
                          ? QStringLiteral("core_run failed (ABI error %1)").arg(rc)
                          : message);
        return;
    }
    if (errorMessage) {
        // A hard failure (unknown command, busy adapter, policy refusal) is
        // reported through error_message rather than as command output.
        const QString message = takeCString(errorMessage);
        emit commandError(requestId, message);
        return;
    }
    const QString stdoutText = takeCString(outStdout);
    const QString stderrText = takeCString(outStderr);
    emit commandFinished(requestId, ok, stdoutText, stderrText, exitCode);
}

void CoreWorker::generateKey(bool force)
{
    if (!m_core) {
        emit keyGenerated(false, QString(), QStringLiteral("control core not initialized"));
        return;
    }
    bool ok = false;
    char *pem = nullptr;
    char *errorMessage = nullptr;
    const CoreError rc = core_generate_key(m_core, force, &ok, &pem, &errorMessage);
    if (rc != CoreError::Ok) {
        const QString message = takeCString(errorMessage);
        emit keyGenerated(false, QString(), message.isEmpty()
                          ? QStringLiteral("core_generate_key failed (ABI error %1)").arg(rc)
                          : message);
        return;
    }
    emit keyGenerated(ok, takeCString(pem), takeCString(errorMessage));
}

void CoreWorker::pair()
{
    if (!m_core) {
        emit paired(false, QString(), QStringLiteral("control core not initialized"));
        return;
    }
    bool ok = false;
    char *out = nullptr;
    char *errorMessage = nullptr;
    const CoreError rc = core_pair(m_core, &ok, &out, &errorMessage);
    if (rc != CoreError::Ok) {
        const QString message = takeCString(errorMessage);
        emit paired(false, QString(), message.isEmpty()
                    ? QStringLiteral("core_pair failed (ABI error %1)").arg(rc)
                    : message);
        return;
    }
    emit paired(ok, takeCString(out), takeCString(errorMessage));
}

void CoreWorker::setConfig(const QString &vin, const QString &model, const QString &keyName,
                           int connectTimeoutSec, int commandTimeoutSec)
{
    if (!m_core) {
        emit configSaved(false, QStringLiteral("control core not initialized"));
        return;
    }
    const QByteArray vinBa = vin.toUtf8();
    const QByteArray modelBa = model.toUtf8();
    const QByteArray keyNameBa = keyName.toUtf8();
    bool ok = false;
    char *errorMessage = nullptr;
    const CoreError rc = core_set_config(m_core, vinBa.constData(), modelBa.constData(),
                                         keyNameBa.constData(), connectTimeoutSec,
                                         commandTimeoutSec, &ok, &errorMessage);
    if (rc != CoreError::Ok) {
        const QString message = takeCString(errorMessage);
        emit configSaved(false, message.isEmpty()
                         ? QStringLiteral("core_set_config failed (ABI error %1)").arg(rc)
                         : message);
        return;
    }
    emit configSaved(ok, takeCString(errorMessage));
}

void CoreWorker::refreshConfig()
{
    if (!m_core) {
        emit configLoaded(QString(), QString(), QString(), 0, 0, false, QString());
        return;
    }
    char *vin = nullptr;
    char *model = nullptr;
    char *keyName = nullptr;
    int32_t connectTimeoutSec = 0;
    int32_t commandTimeoutSec = 0;
    bool hasKey = false;
    char *publicKeyPem = nullptr;
    const CoreError rc = core_get_status(m_core, &vin, &model, &keyName,
                                         &connectTimeoutSec, &commandTimeoutSec,
                                         &hasKey, &publicKeyPem);
    if (rc != CoreError::Ok) {
        emit configLoaded(QString(), QString(), QString(), 0, 0, false, QString());
        return;
    }
    emit configLoaded(takeCString(vin), takeCString(model), takeCString(keyName),
                      connectTimeoutSec, commandTimeoutSec, hasKey, takeCString(publicKeyPem));
}

TeslaClient::TeslaClient(QObject *parent)
    : QObject(parent)
    , m_worker(nullptr)
    , m_helperAvailable(false)
{
    // The worker lives on its own thread so the blocking C ABI calls
    // (core_run/core_pair: up to connect+command+10s, and Pair adds a 95s
    // allowance covering tesla-session's 90s post-add-key-request grace
    // period for the NFC-card tap - see core.rs's pair()) never stall the
    // GUI thread.
    QThread *thread = new QThread(this);
    m_worker = new CoreWorker();
    m_worker->moveToThread(thread);

    connect(m_worker, &CoreWorker::initialized, this, &TeslaClient::onInitialized);
    connect(m_worker, &CoreWorker::commandFinished, this, &TeslaClient::commandFinished);
    connect(m_worker, &CoreWorker::commandError, this, &TeslaClient::commandError);
    connect(m_worker, &CoreWorker::keyGenerated, this, &TeslaClient::keyGenerated);
    connect(m_worker, &CoreWorker::paired, this, &TeslaClient::paired);
    connect(m_worker, &CoreWorker::configSaved, this, &TeslaClient::configSaved);
    connect(m_worker, &CoreWorker::configLoaded, this, &TeslaClient::configLoaded);

    thread->start();

    // State dir: the app's own writable data dir inside Sailjail (the core
    // writes config.json + the keypem there). Create it before core_new so
    // the first SetConfig has somewhere to write.
    const QString stateDir = QStandardPaths::writableLocation(QStandardPaths::AppDataLocation);
    QDir().mkpath(stateDir);

    QMetaObject::invokeMethod(m_worker, "initialize", Qt::QueuedConnection,
                              Q_ARG(QString, QString::fromLatin1(kBinDir)),
                              Q_ARG(QString, stateDir),
                              Q_ARG(QString, QString::fromLatin1(kSessionBin)));

    m_helperVersion = QString::fromUtf8(core_version());
}

TeslaClient::~TeslaClient()
{
    // Stop the worker thread before the core handle goes away. wait() returns
    // once no queued slot is running; the worker is then idle and safe to
    // delete from this thread (no deleteLater, which would need its own loop).
    if (m_worker) {
        QThread *thread = m_worker->thread();
        thread->quit();
        thread->wait();
        delete m_worker;
        m_worker = nullptr;
    }
}

bool TeslaClient::helperAvailable() const
{
    return m_helperAvailable;
}

QString TeslaClient::appVersion() const
{
    return QString::fromLatin1(APP_VERSION);
}

QString TeslaClient::helperVersion() const
{
    return m_helperVersion;
}

void TeslaClient::setHelperAvailable(bool available)
{
    if (m_helperAvailable == available)
        return;
    m_helperAvailable = available;
    emit helperAvailableChanged();
}

void TeslaClient::onInitialized(bool ok, const QString &errorMessage)
{
    if (!ok)
        qWarning("TeslaClient: core init failed: %s", qPrintable(errorMessage));
    setHelperAvailable(ok);
}

void TeslaClient::runCommand(const QString &requestId, const QString &cmd, const QVariantList &args)
{
    QStringList argList;
    argList.reserve(args.size());
    for (const QVariant &v : args)
        argList << v.toString();

    QMetaObject::invokeMethod(m_worker, "runCommand", Qt::QueuedConnection,
                              Q_ARG(QString, requestId),
                              Q_ARG(QString, cmd),
                              Q_ARG(QStringList, argList));
}

void TeslaClient::generateKey(bool force)
{
    QMetaObject::invokeMethod(m_worker, "generateKey", Qt::QueuedConnection,
                              Q_ARG(bool, force));
}

void TeslaClient::pair()
{
    QMetaObject::invokeMethod(m_worker, "pair", Qt::QueuedConnection);
}

void TeslaClient::setConfig(const QString &vin, const QString &model, const QString &keyName,
                            int connectTimeoutSec, int commandTimeoutSec)
{
    QMetaObject::invokeMethod(m_worker, "setConfig", Qt::QueuedConnection,
                              Q_ARG(QString, vin),
                              Q_ARG(QString, model),
                              Q_ARG(QString, keyName),
                              Q_ARG(int, connectTimeoutSec),
                              Q_ARG(int, commandTimeoutSec));
}

void TeslaClient::refreshConfig()
{
    QMetaObject::invokeMethod(m_worker, "refreshConfig", Qt::QueuedConnection);
}

void TeslaClient::refreshHelperAvailable()
{
    // In-process: availability is fixed at core_new time; nothing to probe.
    // Keep the slot so QML callers need no edits.
    emit helperAvailableChanged();
}

void TeslaClient::refreshHelperVersion()
{
    // core_version() is static, but re-emit so callers' refresh flow keeps
    // working (the property may have changed after a re-init in theory).
    emit helperVersionChanged();
}
