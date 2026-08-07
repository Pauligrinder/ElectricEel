#include "teslaclient.h"

#include <QDBusConnection>
#include <QDBusConnectionInterface>
#include <QDBusPendingCall>
#include <QDBusPendingCallWatcher>
#include <QDBusPendingReply>

namespace {
const char *kServiceName = "org.teslacontrol.Helper";
const char *kObjectPath = "/org/teslacontrol/Helper";
const char *kInterfaceName = "org.teslacontrol.Helper1";
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
    QDBusConnectionInterface *bus = QDBusConnection::systemBus().interface();
    setHelperAvailable(bus && bus->isServiceRegistered(kServiceName));
}

void TeslaClient::runCommand(const QString &requestId, const QString &cmd, const QVariantList &args)
{
    QStringList argList;
    for (const QVariant &v : args)
        argList << v.toString();

    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("Run"), cmd, argList);
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
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("Pair"));
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
