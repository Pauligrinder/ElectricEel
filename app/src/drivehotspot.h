#ifndef DRIVEHOTSPOT_H
#define DRIVEHOTSPOT_H

#include <QObject>
#include <QString>
#include <QTimer>

class QDBusInterface;

// Turns the ConnMan Wi-Fi hotspot on when phone-key reports the phone is
// inside the car (very strong RSSI), and off a few minutes after the car
// goes far — only if we enabled it. No GPS. SSID/passphrase are never logged.
class DriveHotspot : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool enabled READ enabled WRITE setEnabled NOTIFY enabledChanged)

public:
    explicit DriveHotspot(QObject *parent = nullptr);
    ~DriveHotspot() override;

    bool enabled() const;
    void setEnabled(bool enabled);

    void onPhoneKeyEvent(const QString &kind);

signals:
    void enabledChanged();

private:
    QDBusInterface *wifiInterface();
    bool tetheringOn(bool *ok = nullptr);
    bool setTethering(bool on);
    void enableForDrive();
    void scheduleDisable();
    void cancelDisable();
    void disableIfOwned();

    bool m_enabled = false;
    bool m_owned = false;
    QTimer m_offTimer;
};

#endif // DRIVEHOTSPOT_H
