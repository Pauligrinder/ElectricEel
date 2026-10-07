#include "phonekeybus.h"

#include <QDBusConnection>
#include <QDBusError>
#include <QDBusMessage>
#include <QTimer>

extern "C" {
#include "electriceelcore.h"
}

namespace {

const char *kService = "org.electriceel.PhoneKey";
const char *kPath = "/org/electriceel/PhoneKey";
const char *kIface = "org.electriceel.PhoneKey";
const int kFarDelayMs = 3 * 60 * 1000;

void keylog(const char *tag, const QString &message)
{
    const QByteArray utf8 = message.toUtf8();
    core_keylog(tag, utf8.constData());
}

} // namespace

PhoneKeyBus::PhoneKeyBus(QObject *parent)
    : QObject(parent)
    , m_farTimer(new QTimer(this))
{
    m_farTimer->setSingleShot(true);
    m_farTimer->setInterval(kFarDelayMs);
    connect(m_farTimer, &QTimer::timeout, this, &PhoneKeyBus::emitDeferredFar);

    QDBusConnection bus = QDBusConnection::sessionBus();
    if (!bus.isConnected()) {
        keylog("dbus", QStringLiteral("session bus unavailable"));
        return;
    }
    // Name ownership is optional: Automagic matches path/interface/signal
    // (see harbour-automagic battery_level_ind example). Own it when
    // Sailjail allows so Destination can be set too.
    if (!bus.registerService(QLatin1String(kService)))
        keylog("dbus", QStringLiteral("registerService: %1")
               .arg(bus.lastError().message()));
}

void PhoneKeyBus::publish(const QString &kind)
{
    if (!kind.startsWith(QLatin1String("presence_")))
        return;

    const QString shortKind = kind.mid(9); // strip "presence_"

    if (shortKind == QLatin1String("far")) {
        if (m_farTimer->isActive())
            return;
        m_farTimer->start();
        keylog("dbus", QStringLiteral("Far in 3m (skip if Inside first)"));
        return;
    }

    if (shortKind == QLatin1String("inside"))
        cancelDeferredFar(QStringLiteral("back inside"));
    else if (shortKind == QLatin1String("stopped"))
        cancelDeferredFar(QStringLiteral("phone-key stopped"));

    sendSignal(QStringLiteral("Presence"), shortKind);

    if (shortKind == QLatin1String("inside"))
        sendSignal(QStringLiteral("Inside"));
    else if (shortKind == QLatin1String("near"))
        sendSignal(QStringLiteral("Near"));
    else if (shortKind == QLatin1String("auth_ok"))
        sendSignal(QStringLiteral("AuthOk"));
    else if (shortKind == QLatin1String("handle_pull"))
        sendSignal(QStringLiteral("HandlePull"));
}

void PhoneKeyBus::emitDeferredFar()
{
    sendSignal(QStringLiteral("Presence"), QStringLiteral("far"));
    sendSignal(QStringLiteral("Far"));
}

void PhoneKeyBus::cancelDeferredFar(const QString &reason)
{
    if (!m_farTimer->isActive())
        return;
    m_farTimer->stop();
    keylog("dbus", QStringLiteral("Far skipped (%1)").arg(reason));
}

void PhoneKeyBus::sendSignal(const QString &name, const QString &arg)
{
    QDBusMessage msg = QDBusMessage::createSignal(
                QLatin1String(kPath),
                QLatin1String(kIface),
                name);
    if (!arg.isEmpty())
        msg << arg;
    if (!QDBusConnection::sessionBus().send(msg)) {
        keylog("dbus", QStringLiteral("send %1 failed").arg(name));
        return;
    }
    if (arg.isEmpty())
        keylog("dbus", name);
    else
        keylog("dbus", QStringLiteral("%1 %2").arg(name, arg));
}
