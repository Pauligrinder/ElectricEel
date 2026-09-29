#ifndef DRIVEAPPLAUNCHER_H
#define DRIVEAPPLAUNCHER_H

#include <QObject>
#include <QString>
#include <QVariantList>

// Launches a chosen .desktop app when phone-key reports the phone is inside
// the car (presence_inside). Native and Android (apkd_launcher_*) apps are
// listed from the standard applications directories.
class DriveAppLauncher : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool enabled READ enabled WRITE setEnabled NOTIFY enabledChanged)
    Q_PROPERTY(QString desktopFile READ desktopFile WRITE setDesktopFile
               NOTIFY desktopFileChanged)
    Q_PROPERTY(QString appName READ appName NOTIFY desktopFileChanged)

public:
    explicit DriveAppLauncher(QObject *parent = nullptr);

    bool enabled() const;
    void setEnabled(bool enabled);

    QString desktopFile() const;
    void setDesktopFile(const QString &path);

    QString appName() const;

    // [{ name, desktopFile, icon }] sorted by name. Includes Android apps.
    Q_INVOKABLE QVariantList installedApps() const;

    void onPhoneKeyEvent(const QString &kind);

signals:
    void enabledChanged();
    void desktopFileChanged();

private:
    void launchForDrive();
    bool launchDesktop(const QString &desktopPath) const;
    static QString readDesktopValue(const QString &path, const QString &key);
    static QString expandExec(const QString &exec, const QString &desktopPath);

    bool m_enabled = false;
    bool m_launchedThisTrip = false;
    QString m_desktopFile;
};

#endif // DRIVEAPPLAUNCHER_H
