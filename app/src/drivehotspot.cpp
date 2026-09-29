#include "drivehotspot.h"

#include <QDBusConnection>
#include <QDBusInterface>
#include <QDBusReply>
#include <QDBusVariant>
#include <QSettings>
#include <QVariant>

#include "uisettings.h"

extern "C" {
#include "electriceelcore.h"
}

namespace {

const char *kSettingsGroup = "DriveHotspot";
const char *kEnabledKey = "enabled";
const int kOffDelayMs = 3 * 60 * 1000;

void keylog(const char *tag, const QString &message)
{
    const QByteArray utf8 = message.toUtf8();
    core_keylog(tag, utf8.constData());
}

void hotspotSettingsBegin(QSettings *s)
{
    s->beginGroup(QLatin1String(kSettingsGroup));
}

} // namespace

DriveHotspot::DriveHotspot(QObject *parent)
    : QObject(parent)
{
    m_offTimer.setSingleShot(true);
    connect(&m_offTimer, &QTimer::timeout, this, &DriveHotspot::disableIfOwned);

    QSettings s(uiSettingsPath(), QSettings::IniFormat);
    hotspotSettingsBegin(&s);
    m_enabled = s.value(QLatin1String(kEnabledKey), false).toBool();
}

DriveHotspot::~DriveHotspot()
{
    m_offTimer.stop();
}

bool DriveHotspot::enabled() const
{
    return m_enabled;
}

void DriveHotspot::setEnabled(bool enabled)
{
    if (m_enabled == enabled)
        return;
    m_enabled = enabled;
    QSettings s(uiSettingsPath(), QSettings::IniFormat);
    hotspotSettingsBegin(&s);
    s.setValue(QLatin1String(kEnabledKey), enabled);
    s.sync();
    if (!enabled) {
        cancelDisable();
        disableIfOwned();
    }
    emit enabledChanged();
}

void DriveHotspot::onPhoneKeyEvent(const QString &kind)
{
    if (kind == QLatin1String("presence_inside")) {
        cancelDisable();
        if (m_enabled)
            enableForDrive();
        return;
    }
    if (kind == QLatin1String("presence_far")) {
        if (m_owned)
            scheduleDisable();
        return;
    }
    if (kind == QLatin1String("presence_near")
            || kind == QLatin1String("presence_auth_ok")) {
        cancelDisable();
    }
}

QDBusInterface *DriveHotspot::wifiInterface()
{
    QDBusInterface *iface = new QDBusInterface(
                QStringLiteral("net.connman"),
                QStringLiteral("/net/connman/technology/wifi"),
                QStringLiteral("net.connman.Technology"),
                QDBusConnection::systemBus(),
                this);
    if (!iface->isValid()) {
        keylog("hotspot", QStringLiteral("wifi technology unavailable: %1")
               .arg(iface->lastError().message()));
        delete iface;
        return nullptr;
    }
    return iface;
}

bool DriveHotspot::tetheringOn(bool *ok)
{
    QDBusInterface *iface = wifiInterface();
    if (!iface) {
        if (ok)
            *ok = false;
        return false;
    }
    const QDBusReply<QVariantMap> reply = iface->call(QStringLiteral("GetProperties"));
    iface->deleteLater();
    if (!reply.isValid()) {
        keylog("hotspot", QStringLiteral("GetProperties failed: %1").arg(reply.error().message()));
        if (ok)
            *ok = false;
        return false;
    }
    if (ok)
        *ok = true;
    return reply.value().value(QStringLiteral("Tethering")).toBool();
}

bool DriveHotspot::setTethering(bool on)
{
    QDBusInterface *iface = wifiInterface();
    if (!iface)
        return false;
    const QDBusMessage reply = iface->call(
                QStringLiteral("SetProperty"),
                QStringLiteral("Tethering"),
                QVariant::fromValue(QDBusVariant(on)));
    iface->deleteLater();
    if (reply.type() == QDBusMessage::ErrorMessage) {
        keylog("hotspot", QStringLiteral("SetProperty Tethering failed: %1").arg(reply.errorMessage()));
        return false;
    }
    return true;
}

void DriveHotspot::enableForDrive()
{
    bool readOk = false;
    const bool alreadyOn = tetheringOn(&readOk);
    if (!readOk)
        return;
    if (alreadyOn) {
        if (!m_owned)
            keylog("hotspot", QStringLiteral("already on - leaving as found"));
        return;
    }
    if (!setTethering(true))
        return;
    m_owned = true;
    keylog("hotspot", QStringLiteral("enabled inside the car"));
}

void DriveHotspot::scheduleDisable()
{
    if (!m_owned || m_offTimer.isActive())
        return;
    keylog("hotspot", QStringLiteral("car far - hotspot off in 3m if we enabled it"));
    m_offTimer.start(kOffDelayMs);
}

void DriveHotspot::cancelDisable()
{
    if (m_offTimer.isActive()) {
        m_offTimer.stop();
        keylog("hotspot", QStringLiteral("off timer cancelled"));
    }
}

void DriveHotspot::disableIfOwned()
{
    if (!m_owned)
        return;
    m_owned = false;
    bool readOk = false;
    const bool on = tetheringOn(&readOk);
    if (!readOk || !on) {
        keylog("hotspot", QStringLiteral("release without changing radio"));
        return;
    }
    if (setTethering(false))
        keylog("hotspot", QStringLiteral("disabled after walk-away"));
}
