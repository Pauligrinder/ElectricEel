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

// APP_VERSION is defined from $${VERSION} in harbour-teslacontrol.pro. The
// release workflow stamps both that and the helper's Cargo.toml from the
// same git tag, so a matching app+helper pair reports equal versions; the
// Settings page and the FirstPage banner use the comparison to surface
// installs that drifted apart (see KNOWN_ISSUES.md "0.1.6 mismatch").
#ifndef APP_VERSION
#define APP_VERSION "0.0.0"
#endif

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
    refreshHelperVersion();
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

void TeslaClient::refreshHelperVersion()
{
    // Best-effort: GetVersion exists only in helpers that report their own
    // version (0.1.7+). Calling it against an older helper returns
    // UnknownMethod - which is itself the signal the UI needs: a helper
    // that cannot name its version predates the `model` field responsible
    // for the 0.1.6 app/helper interface break. On error the version stays
    // "" (= "too old / unknown") and the UI says so.
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("GetVersion"));
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<QString> reply = *w;
        QString version;
        if (!reply.isError())
            version = reply.argumentAt<0>().trimmed();
        if (version != m_helperVersion) {
            m_helperVersion = version;
            emit helperVersionChanged();
        }
        w->deleteLater();
    });
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
        QDBusPendingReply<QString, QString, QString, int, int, bool, QString> reply = *w;
        if (reply.isError()) {
            const QDBusError &err = reply.error();
            const bool serviceMissing =
                    err.type() == QDBusError::ServiceUnknown
                    || err.name() == QLatin1String("org.freedesktop.DBus.Error.ServiceDoesNotExist");
            setHelperAvailable(!serviceMissing);
        } else {
            setHelperAvailable(true);
        }
        // Keep the version in lockstep with availability: whenever the
        // presence probe runs (startup, pull-to-refresh, error recovery)
        // re-ask the version too, so an upgrade of the running helper is
        // picked up without relaunching the app.
        refreshHelperVersion();
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

void TeslaClient::setConfig(const QString &vin, const QString &model, const QString &keyName, int connectTimeoutSec, int commandTimeoutSec)
{
    QDBusPendingCall pcall = m_iface->asyncCall(QStringLiteral("SetConfig"), vin, model, keyName, connectTimeoutSec, commandTimeoutSec);
    auto *watcher = new QDBusPendingCallWatcher(pcall, this);
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this](QDBusPendingCallWatcher *w) {
        QDBusPendingReply<bool, QString> reply = *w;
        if (reply.isError()) {
            refreshHelperAvailable();
            // A helper at a different version rejects this method's argument
            // list outright - e.g. a pre-0.1.6 daemon gets `(sssii)` where
            // it expects `(ssii)` and errors with a zbus "Signature
            // mismatch" (or Qt reports InvalidSignature). Translate that
            // into an actionable message instead of the raw D-Bus text.
            const QString &errName = reply.error().name();
            const QString &errText = reply.error().message();
            QString message = errText;
            if (errName == QLatin1String("org.freedesktop.DBus.Error.InvalidSignature")
                    || errText.contains(QLatin1String("signature"), Qt::CaseInsensitive)) {
                message = QStringLiteral("teslacontrold is too old for this app version - update the helper package to 0.1.6 or later.");
            }
            emit configSaved(false, message);
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
        QDBusMessage msg = w->reply();
        // GetConfig's reply gained a `model` field at 0.1.6: current
        // helpers return (vin, model, key_name, connect_timeout_sec,
        // command_timeout_sec, has_key, public_key_pem) = signature
        // "sssiibs"; pre-0.1.6 helpers return the same without model =
        // "ssiibs". A typed QDBusPendingReply treats either mismatch as an
        // error and yields *empty* arguments - so before this change a
        // helper a version behind silently made configLoaded never fire,
        // which surfaced as "No VIN configured" even though config.json had
        // one. Parse by the actual reply signature instead, so an app and
        // helper mid-upgrade (e.g. right after installing only the app RPM)
        // still display the VIN/hasKey; SetConfig's translated error below
        // is what tells the user the helper itself still needs updating.
        // Parsed via arguments() rather than QDBusPendingReply(QDBusMessage)
        // to keep it working across the Qt versions the target SDK ships.
        const QString &sig = msg.signature();
        const QList<QVariant> args = msg.arguments();
        if (sig == QLatin1String("sssiibs") && args.size() == 7) {
            emit configLoaded(args.value(0).toString(), args.value(1).toString(), args.value(2).toString(),
                              args.value(3).toInt(), args.value(4).toInt(), args.value(5).toBool(),
                              args.value(6).toString());
        } else if (sig == QLatin1String("ssiibs") && args.size() == 6) {
            // Pre-0.1.6 helper: (vin, key_name, connect, command, has_key, public_key_pem).
            qWarning("TeslaClient: helper reports a pre-0.1.6 GetConfig layout (no model field); "
                     "update teslacontrold to 0.1.6+ to unify versions");
            emit configLoaded(args.value(0).toString(), QString(), args.value(1).toString(),
                              args.value(2).toInt(), args.value(3).toInt(), args.value(4).toBool(),
                              args.value(5).toString());
        } else {
            // Error reply, or an unexpected layout from a future change.
            refreshHelperAvailable();
        }
        w->deleteLater();
    });
}
