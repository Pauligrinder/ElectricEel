#include "cpukeepalive.h"

#include <QDate>
#include <QDateTime>
#include <QDBusConnection>
#include <QDBusInterface>
#include <QDBusMessage>
#include <QFile>
#include <QMutexLocker>
#include <QVariant>

namespace {

const char *kMceService = "com.nokia.mce";
const char *kMcePath = "/com/nokia/mce/request";
const char *kMceInterface = "com.nokia.mce.request";
const char *kLeaseId = "harbour-electric-eel-phone-key";
const int kFallbackPeriodSec = 60;
const int kDbusTimeoutMs = 5000;

// Half the lease, then clamped to 5-20s, and always short enough that two
// intervals still finish before the lease expires. One late tick therefore
// cannot drop late-suspend prevention.
int keepaliveRenewMs(int periodSec)
{
    if (periodSec <= 0)
        periodSec = kFallbackPeriodSec;
    const int periodMs = periodSec * 1000;
    int delayMs = periodMs / 2;
    constexpr int kMinMs = 5000;
    constexpr int kMaxMs = 20000;
    if (delayMs > kMaxMs)
        delayMs = kMaxMs;
    if (delayMs < kMinMs)
        delayMs = kMinMs;
    const int missedTickCeiling = (periodMs - 1000) / 2;
    if (missedTickCeiling > 0 && delayMs > missedTickCeiling)
        delayMs = missedTickCeiling;
    if (delayMs < 1000)
        delayMs = 1000;
    return delayMs;
}

QString dbusFailure(const QDBusMessage &reply)
{
    if (reply.type() == QDBusMessage::ErrorMessage)
        return reply.errorName() + QStringLiteral(": ") + reply.errorMessage();
    return QStringLiteral("no reply");
}

int queryPeriodSec(QDBusInterface &iface)
{
    if (!iface.isValid()) {
        logPhoneKeyLine(QStringLiteral("keepalive"),
                        QStringLiteral("cpu keepalive period query failed: ")
                        + iface.lastError().name() + QStringLiteral(": ")
                        + iface.lastError().message());
        return kFallbackPeriodSec;
    }
    const QDBusMessage reply = iface.call(QStringLiteral("req_cpu_keepalive_period"));
    if (reply.type() != QDBusMessage::ReplyMessage || reply.arguments().isEmpty()) {
        logPhoneKeyLine(QStringLiteral("keepalive"),
                        QStringLiteral("cpu keepalive period query failed: ") + dbusFailure(reply));
        return kFallbackPeriodSec;
    }
    bool ok = false;
    const int period = reply.arguments().at(0).toInt(&ok);
    if (!ok || period <= 0) {
        logPhoneKeyLine(QStringLiteral("keepalive"),
                        QStringLiteral("cpu keepalive period query failed: invalid period"));
        return kFallbackPeriodSec;
    }
    return period;
}

bool callLease(QDBusInterface &iface, const char *method, QString *error)
{
    if (!iface.isValid()) {
        if (error)
            *error = iface.lastError().name() + QStringLiteral(": ") + iface.lastError().message();
        return false;
    }
    const QDBusMessage reply = iface.call(
            QString::fromLatin1(method), QString::fromLatin1(kLeaseId));
    if (reply.type() != QDBusMessage::ReplyMessage) {
        if (error)
            *error = dbusFailure(reply);
        return false;
    }
    if (!reply.arguments().isEmpty()
            && reply.arguments().at(0).type() == QVariant::Bool
            && !reply.arguments().at(0).toBool()) {
        if (error)
            *error = QStringLiteral("refused");
        return false;
    }
    return true;
}

} // namespace

void logPhoneKeyLine(const QString &tag, const QString &event)
{
    static QMutex mutex;
    const QMutexLocker locker(&mutex);
    const QString logDir = QString::fromUtf8(qgetenv("ELECTRIC_EEL_LOG_DIR"));
    if (logDir.isEmpty())
        return;
    const QString path = logDir + QStringLiteral("/phone-key-")
            + QDate::currentDate().toString(QStringLiteral("yyyy-MM-dd")) + QStringLiteral(".log");
    const bool newFile = !QFile::exists(path);
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Append))
        return;
    if (newFile) {
        file.write("# ElectricEel phone-key log\n"
                   "# tags: session presence connect auth link bluez core ui keepalive\n");
    }
    const QString field = tag.left(12).leftJustified(12, QLatin1Char(' '));
    file.write(QDateTime::currentDateTime().toString(QStringLiteral("HH:mm:ss.zzz")).toUtf8()
               + "  " + field.toUtf8() + event.toUtf8() + "\n");
}

CpuKeepAlive::CpuKeepAlive()
    : m_worker(this)
{
}

CpuKeepAlive::~CpuKeepAlive()
{
    setEnabled(false);
}

void CpuKeepAlive::setEnabled(bool enabled)
{
    {
        const QMutexLocker locker(&m_mutex);
        if (enabled == m_enabled)
            return;
        m_enabled = enabled;
        m_stop = !enabled;
        m_wait.wakeAll();
    }
    if (enabled) {
        if (!m_worker.isRunning())
            m_worker.start();
    } else {
        m_worker.wait();
    }
}

bool CpuKeepAlive::stopRequested() const
{
    const QMutexLocker locker(&m_mutex);
    return m_stop;
}

bool CpuKeepAlive::waitForRenew(int renewMs)
{
    QMutexLocker locker(&m_mutex);
    if (m_stop)
        return false;
    m_wait.wait(&m_mutex, static_cast<unsigned long>(renewMs));
    return !m_stop;
}

void CpuKeepAlive::Worker::run()
{
    m_owner->execute();
}

void CpuKeepAlive::execute()
{
    // Private connection owned by this thread. systemBus() may already live
    // on the GUI thread, and a frozen GUI event loop would then swallow the
    // renewal replies. Blocking call() runs a local loop on this thread.
    const QString busName = QStringLiteral("harbour-electric-eel-cpu-keepalive");
    const QDBusConnection bus = QDBusConnection::connectToBus(QDBusConnection::SystemBus, busName);
    {
        QDBusInterface iface(QString::fromLatin1(kMceService),
                             QString::fromLatin1(kMcePath),
                             QString::fromLatin1(kMceInterface),
                             bus);
        iface.setTimeout(kDbusTimeoutMs);
        const int periodSec = queryPeriodSec(iface);
        const int renewMs = keepaliveRenewMs(periodSec);
        logPhoneKeyLine(QStringLiteral("keepalive"),
                        QStringLiteral("cpu keepalive period=%1s renew=%2s")
                        .arg(periodSec)
                        .arg(renewMs / 1000));
        bool held = false;
        while (!stopRequested()) {
            QString error;
            if (callLease(iface, "req_cpu_keepalive_start", &error)) {
                if (!held) {
                    logPhoneKeyLine(QStringLiteral("keepalive"), QStringLiteral("cpu keepalive held"));
                    held = true;
                }
            } else {
                logPhoneKeyLine(QStringLiteral("keepalive"),
                                QStringLiteral("cpu keepalive start failed: ") + error);
            }
            if (!waitForRenew(renewMs))
                break;
        }
        QString error;
        if (callLease(iface, "req_cpu_keepalive_stop", &error))
            logPhoneKeyLine(QStringLiteral("keepalive"), QStringLiteral("cpu keepalive released"));
        else
            logPhoneKeyLine(QStringLiteral("keepalive"),
                            QStringLiteral("cpu keepalive stop failed: ") + error);
    }
    QDBusConnection::disconnectFromBus(busName);
}
