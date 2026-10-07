#ifndef PHONEKEYBUS_H
#define PHONEKEYBUS_H

#include <QObject>
#include <QString>

class QTimer;

// Session-bus phone-key signals for harbour-automagic (and anything else
// that AddMatch's org.electriceel.PhoneKey). ElectricEel does not toggle
// tethering or launch apps.
//
// Far is held for 3 minutes and dropped if Inside arrives first, so a
// brief walk-away does not fire Automagic hotspot-off.
class PhoneKeyBus : public QObject
{
    Q_OBJECT

public:
    explicit PhoneKeyBus(QObject *parent = nullptr);

    void publish(const QString &kind);

private:
    void sendSignal(const QString &name, const QString &arg = QString());
    void emitDeferredFar();
    void cancelDeferredFar(const QString &reason);

    QTimer *m_farTimer = nullptr;
};

#endif // PHONEKEYBUS_H
