#include "teslaclient.h"

#include <QDBusConnection>
#include <QDBusConnectionInterface>
#include <QDBusError>
#include <QDBusMessage>
#include <QDBusPendingCall>
#include <QDBusPendingCallWatcher>
#include <QDBusPendingReply>

namespace {
const char *kServiceName = "org.teslacontrol.Helper";
const char *kObjectPath = "/org/teslacontrol/Helper";
const char *kInterfaceName = "org.teslacontrol.Helper1";

// QDBusInterface::asyncCall has no per-call timeout of its own - it (and
// the Qt/libdbus default, ~25s) is fine for GetConfig/SetConfig/GenerateKey,
// which never touch BLE, but Run and Pair can legitimately take far longer:
// helper/src/helper.rs sizes its own subprocess deadline as
// connect_timeout_sec + command_timeout_sec + 10 (Run) or that plus a 30s
// NFC-tap allowance (Pair), and connect/command timeouts are configurable
// up to config::MAX_TIMEOUT_SEC (300s) each. Sized to the larger of the
// two (Pair's) worst case - 300+300+10+30=640s - with a little slack, so
// the client-side timeout is never shorter than what the server could
// legitimately still be doing. Built as a raw QDBusMessage + a dedicated
// asyncCall(..., timeout) instead of raising m_iface's interface-wide
// timeout, so the fast calls above keep failing fast if the service itself
// is down.
const int kLongCallTimeoutMs = 650000;
}

TeslaClient::TeslaClient(QObject *parent)
    : QObject(parent)
    , m_iface(new QDBusInterface(kServiceName, kObjectPath, kInterfaceName, QDBusConnection::systemBus(), this))
    , m_helperAvailable(false)
{
    refreshHelperAvailable();
}

bool TeslaClient::helperAvailable() const
{
    return m_helperAvailable;
}

void TeslaClient::setHelperAvailable(bool available)
{
    if (m_helperAvailable == available)
        return;
    m_helperAvailable = available;
    emit helperAvailableChanged();
}

void TeslaClient::refreshHelperAvailable()
{
    if (!QDBusConnection::systemBus().isConnected()) {
        setHelperAvailable(false);
        return;
    }
    // A name-registration query (isServiceRegistered) is blocked by the
    // Sailjail sandbox proxy, so probe with a real call instead. A reply -
    // even an error - proves the service is there; only "no such service"
    // means it isn't.
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("GetConfig"));
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<QString, QString, int, int, bool, QString> reply = *w;
        if (reply.isError()) {
            const QDBusError &err = reply.error();
            const bool serviceMissing =
                    err.type() == QDBusError::ServiceUnknown
                    || err.name() == QLatin1String("org.freedesktop.DBus.Error.ServiceDoesNotExist");
            setHelperAvailable(!serviceMissing);
        } else {
            setHelperAvailable(true);
        }
        w->deleteLater();
    });
}

void TeslaClient::runCommand(const QString &requestId, const QString &cmd, const QVariantList &args)
{
    QStringList argList;
    for (const QVariant &v : args)
        argList << v.toString();

    QDBusMessage msg = QDBusMessage::createMethodCall(kServiceName, kObjectPath, kInterfaceName, QStringLiteral("Run"));
    msg << cmd << QVariant::fromValue(argList);
    QDBusPendingCall pcall = QDBusConnection::systemBus().asyncCall(msg, kLongCallTimeoutMs);
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this, requestId](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<bool, QString, QString, int> reply = *w;
        if (reply.isError()) {
            refreshHelperAvailable();
            emit commandError(requestId, reply.error().message());
        } else {
            emit commandFinished(requestId, reply.argumentAt<0>(), reply.argumentAt<1>(),
                                  reply.argumentAt<2>(), reply.argumentAt<3>());
        }
        w->deleteLater();
    });
}

void TeslaClient::generateKey(bool force)
{
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("GenerateKey"), force);
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<bool, QString, QString> reply = *w;
        if (reply.isError()) {
            refreshHelperAvailable();
            emit keyGenerated(false, QString(), reply.error().message());
        } else {
            emit keyGenerated(reply.argumentAt<0>(), reply.argumentAt<1>(), reply.argumentAt<2>());
        }
        w->deleteLater();
    });
}

void TeslaClient::pair()
{
    QDBusMessage msg = QDBusMessage::createMethodCall(kServiceName, kObjectPath, kInterfaceName, QStringLiteral("Pair"));
    QDBusPendingCall pcall = QDBusConnection::systemBus().asyncCall(msg, kLongCallTimeoutMs);
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<bool, QString, QString> reply = *w;
        if (reply.isError()) {
            refreshHelperAvailable();
            emit paired(false, QString(), reply.error().message());
        } else {
            emit paired(reply.argumentAt<0>(), reply.argumentAt<1>(), reply.argumentAt<2>());
        }
        w->deleteLater();
    });
}

void TeslaClient::setConfig(const QString &vin, const QString &keyName, int connectTimeoutSec, int commandTimeoutSec)
{
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("SetConfig"), vin, keyName, connectTimeoutSec, commandTimeoutSec);
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<bool, QString> reply = *w;
        if (reply.isError()) {
            refreshHelperAvailable();
            emit configSaved(false, reply.error().message());
        } else {
            emit configSaved(reply.argumentAt<0>(), reply.argumentAt<1>());
        }
        w->deleteLater();
    });
}

void TeslaClient::refreshConfig()
{
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("GetConfig"));
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<QString, QString, int, int, bool, QString> reply = *w;
        if (reply.isError()) {
            refreshHelperAvailable();
        } else {
            emit configLoaded(reply.argumentAt<0>(), reply.argumentAt<1>(), reply.argumentAt<2>(),
                               reply.argumentAt<3>(), reply.argumentAt<4>(), reply.argumentAt<5>());
        }
        w->deleteLater();
    });
}
