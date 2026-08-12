#ifndef TESLACLIENT_H
#define TESLACLIENT_H

#include <QObject>
#include <QVariantList>
#include <QDBusInterface>

// Thin async client for the privileged org.teslacontrol.Helper system D-Bus
// service (see helper/main.go). This app runs sandboxed under Sailjail and
// has no CAP_NET_ADMIN of its own, so every vehicle command is delegated to
// that service - this class is the only place that fact is visible.
class TeslaClient : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool helperAvailable READ helperAvailable NOTIFY helperAvailableChanged)
    Q_PROPERTY(QString appVersion READ appVersion CONSTANT)
    // "" until GetVersion answers: a helper with no GetVersion (built
    // before 0.1.7) or an unreachable bus leaves it empty, which the UI
    // surfaces as "helper older than this app" rather than a failed call.
    Q_PROPERTY(QString helperVersion READ helperVersion NOTIFY helperVersionChanged)

public:
    explicit TeslaClient(QObject *parent = nullptr);

    bool helperAvailable() const;
    // APP_VERSION, defined from $${VERSION} in harbour-teslacontrol.pro.
    QString appVersion() const;
    QString helperVersion() const;

public slots:
    // requestId is caller-chosen (e.g. a uuid or the command name) and is
    // echoed back on commandFinished/commandError so QML can match replies
    // to the button/dialog that triggered them.
    void runCommand(const QString &requestId, const QString &cmd, const QVariantList &args);

    void generateKey(bool force);
    void pair();
    void setConfig(const QString &vin, const QString &model, const QString &keyName, int connectTimeoutSec, int commandTimeoutSec);
    void refreshConfig();
    void refreshHelperAvailable();
    void refreshHelperVersion();

signals:
    void commandFinished(const QString &requestId, bool ok, const QString &stdOut, const QString &stdErr, int exitCode);
    void commandError(const QString &requestId, const QString &message);

    void keyGenerated(bool ok, const QString &publicKeyPem, const QString &errorMessage);
    void paired(bool ok, const QString &output, const QString &errorMessage);
    void configSaved(bool ok, const QString &errorMessage);
    void configLoaded(const QString &vin, const QString &model, const QString &keyName, int connectTimeoutSec, int commandTimeoutSec,
                       bool hasKey, const QString &publicKeyPem);

    void helperAvailableChanged();
    void helperVersionChanged();

private:
    QDBusInterface *m_iface;
    bool m_helperAvailable;
    QString m_helperVersion;

    void setHelperAvailable(bool available);
};

#endif // TESLACLIENT_H
