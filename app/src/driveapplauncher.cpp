#include "driveapplauncher.h"

#include <QDBusArgument>
#include <QDBusConnection>
#include <QDBusMessage>
#include <QDBusMetaType>
#include <QDBusReply>
#include <QDBusVariant>
#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QProcess>
#include <QSet>
#include <QSettings>
#include <QStandardPaths>
#include <QTextStream>
#include <QVariantMap>

#include <algorithm>

#include "uisettings.h"

extern "C" {
#include "electriceelcore.h"
}

namespace {

const char *kSettingsGroup = "DriveAppLauncher";
const char *kEnabledKey = "enabled";
const char *kDesktopKey = "desktopFile";

void keylog(const char *tag, const QString &message)
{
    const QByteArray utf8 = message.toUtf8();
    core_keylog(tag, utf8.constData());
}

void settingsBegin(QSettings *s)
{
    s->beginGroup(QLatin1String(kSettingsGroup));
}

bool desktopTruthy(const QString &value)
{
    const QString v = value.trimmed().toLower();
    return v == QLatin1String("true") || v == QLatin1String("1");
}

} // namespace

// systemd StartTransientUnit: ExecStart=a(sasb), properties=a(sv),
// aux=a(sa(sv)).
struct SystemdExecStart {
    QString path;
    QStringList args;
    bool ignoreFailure = false;
};

struct SystemdProperty {
    QString name;
    QDBusVariant value;
};

struct SystemdAuxUnit {
    QString name;
    QList<SystemdProperty> properties;
};

QDBusArgument &operator<<(QDBusArgument &arg, const SystemdExecStart &e)
{
    arg.beginStructure();
    arg << e.path << e.args << e.ignoreFailure;
    arg.endStructure();
    return arg;
}

const QDBusArgument &operator>>(const QDBusArgument &arg, SystemdExecStart &e)
{
    arg.beginStructure();
    arg >> e.path >> e.args >> e.ignoreFailure;
    arg.endStructure();
    return arg;
}

QDBusArgument &operator<<(QDBusArgument &arg, const SystemdProperty &p)
{
    arg.beginStructure();
    arg << p.name << p.value;
    arg.endStructure();
    return arg;
}

const QDBusArgument &operator>>(const QDBusArgument &arg, SystemdProperty &p)
{
    arg.beginStructure();
    arg >> p.name >> p.value;
    arg.endStructure();
    return arg;
}

QDBusArgument &operator<<(QDBusArgument &arg, const SystemdAuxUnit &u)
{
    arg.beginStructure();
    arg << u.name << u.properties;
    arg.endStructure();
    return arg;
}

const QDBusArgument &operator>>(const QDBusArgument &arg, SystemdAuxUnit &u)
{
    arg.beginStructure();
    arg >> u.name >> u.properties;
    arg.endStructure();
    return arg;
}

Q_DECLARE_METATYPE(SystemdExecStart)
Q_DECLARE_METATYPE(QList<SystemdExecStart>)
Q_DECLARE_METATYPE(SystemdProperty)
Q_DECLARE_METATYPE(QList<SystemdProperty>)
Q_DECLARE_METATYPE(SystemdAuxUnit)
Q_DECLARE_METATYPE(QList<SystemdAuxUnit>)

namespace {

void registerSystemdTypes()
{
    static bool done = false;
    if (done)
        return;
    qDBusRegisterMetaType<SystemdExecStart>();
    qDBusRegisterMetaType<QList<SystemdExecStart>>();
    qDBusRegisterMetaType<SystemdProperty>();
    qDBusRegisterMetaType<QList<SystemdProperty>>();
    qDBusRegisterMetaType<SystemdAuxUnit>();
    qDBusRegisterMetaType<QList<SystemdAuxUnit>>();
    done = true;
}

// Sailjail blocks exec of systemd-run from inside the app. AppLaunch allows
// session-bus talk to org.freedesktop.systemd1 — create a transient user
// service that runs the desktop Exec outside our sandbox.
bool startTransientShell(const QString &command, QString *errorOut)
{
    registerSystemdTypes();

    SystemdExecStart exec;
    exec.path = QStringLiteral("/bin/sh");
    exec.args = QStringList{
        QStringLiteral("/bin/sh"),
        QStringLiteral("-c"),
        command
    };

    QList<SystemdProperty> props;
    props.append({ QStringLiteral("Description"),
                   QDBusVariant(QStringLiteral("ElectricEel app launch")) });
    props.append({ QStringLiteral("CollectMode"),
                   QDBusVariant(QStringLiteral("inactive")) });
    props.append({ QStringLiteral("ExecStart"),
                   QDBusVariant(QVariant::fromValue(
                                    QList<SystemdExecStart>() << exec)) });

    const QString unit = QStringLiteral("eel-launch-%1.service")
            .arg(QDateTime::currentMSecsSinceEpoch());

    QDBusMessage msg = QDBusMessage::createMethodCall(
                QStringLiteral("org.freedesktop.systemd1"),
                QStringLiteral("/org/freedesktop/systemd1"),
                QStringLiteral("org.freedesktop.systemd1.Manager"),
                QStringLiteral("StartTransientUnit"));
    msg << unit
        << QStringLiteral("replace")
        << QVariant::fromValue(props)
        << QVariant::fromValue(QList<SystemdAuxUnit>());

    const QDBusMessage reply = QDBusConnection::sessionBus().call(msg);
    if (reply.type() == QDBusMessage::ErrorMessage) {
        if (errorOut)
            *errorOut = reply.errorMessage();
        return false;
    }
    return true;
}

} // namespace

DriveAppLauncher::DriveAppLauncher(QObject *parent)
    : QObject(parent)
{
    QSettings s(uiSettingsPath(), QSettings::IniFormat);
    settingsBegin(&s);
    m_enabled = s.value(QLatin1String(kEnabledKey), false).toBool();
    m_desktopFile = s.value(QLatin1String(kDesktopKey)).toString();
}

bool DriveAppLauncher::enabled() const
{
    return m_enabled;
}

void DriveAppLauncher::setEnabled(bool enabled)
{
    if (m_enabled == enabled)
        return;
    m_enabled = enabled;
    QSettings s(uiSettingsPath(), QSettings::IniFormat);
    settingsBegin(&s);
    s.setValue(QLatin1String(kEnabledKey), enabled);
    s.sync();
    if (!enabled)
        m_launchedThisTrip = false;
    emit enabledChanged();
}

QString DriveAppLauncher::desktopFile() const
{
    return m_desktopFile;
}

void DriveAppLauncher::setDesktopFile(const QString &path)
{
    if (m_desktopFile == path)
        return;
    m_desktopFile = path;
    QSettings s(uiSettingsPath(), QSettings::IniFormat);
    settingsBegin(&s);
    s.setValue(QLatin1String(kDesktopKey), path);
    s.sync();
    emit desktopFileChanged();
}

QString DriveAppLauncher::appName() const
{
    if (m_desktopFile.isEmpty())
        return QString();
    const QString name = readDesktopValue(m_desktopFile, QStringLiteral("Name"));
    if (!name.isEmpty())
        return name;
    return QFileInfo(m_desktopFile).completeBaseName();
}

void DriveAppLauncher::onPhoneKeyEvent(const QString &kind)
{
    if (kind == QLatin1String("presence_inside")) {
        if (m_enabled && !m_launchedThisTrip)
            launchForDrive();
        return;
    }
    if (kind == QLatin1String("presence_far")) {
        m_launchedThisTrip = false;
    }
}

void DriveAppLauncher::launchForDrive()
{
    if (m_desktopFile.isEmpty()) {
        keylog("driveapp", QStringLiteral("enabled but no app selected"));
        return;
    }
    if (!QFileInfo::exists(m_desktopFile)) {
        keylog("driveapp", QStringLiteral("desktop missing: %1").arg(m_desktopFile));
        return;
    }
    if (!launchDesktop(m_desktopFile))
        return;
    m_launchedThisTrip = true;
    keylog("driveapp", QStringLiteral("launched %1").arg(appName()));
}

bool DriveAppLauncher::launchDesktop(const QString &desktopPath) const
{
    const QString exec = readDesktopValue(desktopPath, QStringLiteral("Exec"));
    if (exec.isEmpty()) {
        keylog("driveapp", QStringLiteral("no Exec in %1").arg(desktopPath));
        return false;
    }
    const QString expanded = expandExec(exec, desktopPath);

    // Best-effort Lipstick launching animation.
    QProcess::startDetached(QStringLiteral("dbus-send"), {
        QStringLiteral("--session"),
        QStringLiteral("--type=method_call"),
        QStringLiteral("--dest=org.nemomobile.lipstick"),
        QStringLiteral("/LauncherModel"),
        QStringLiteral("org.nemomobile.lipstick.LauncherModel.notifyLaunching"),
        QStringLiteral("string:") + desktopPath
    });

    QString error;
    if (!startTransientShell(expanded, &error)) {
        keylog("driveapp", QStringLiteral("systemd StartTransientUnit failed: %1")
               .arg(error.isEmpty() ? QStringLiteral("(no detail)") : error));
        return false;
    }
    return true;
}

QString DriveAppLauncher::readDesktopValue(const QString &path, const QString &key)
{
    QFile file(path);
    if (!file.open(QIODevice::ReadOnly | QIODevice::Text))
        return QString();
    QTextStream in(&file);
    in.setCodec("UTF-8");
    bool inDesktopEntry = false;
    const QString prefix = key + QLatin1Char('=');
    while (!in.atEnd()) {
        const QString line = in.readLine().trimmed();
        if (line.isEmpty() || line.startsWith(QLatin1Char('#')))
            continue;
        if (line.startsWith(QLatin1Char('['))) {
            inDesktopEntry = (line == QLatin1String("[Desktop Entry]"));
            continue;
        }
        if (!inDesktopEntry || !line.startsWith(prefix))
            continue;
        return line.mid(prefix.size()).trimmed();
    }
    return QString();
}

QString DriveAppLauncher::expandExec(const QString &exec, const QString &desktopPath)
{
    QString out = exec;
    out.replace(QStringLiteral("%i"), QString());
    out.replace(QStringLiteral("%c"), QString());
    out.replace(QStringLiteral("%k"), desktopPath);
    out.replace(QStringLiteral("%f"), QString());
    out.replace(QStringLiteral("%F"), QString());
    out.replace(QStringLiteral("%u"), QString());
    out.replace(QStringLiteral("%U"), QString());
    out.replace(QStringLiteral("%%"), QStringLiteral("%"));
    return out.trimmed();
}

QVariantList DriveAppLauncher::installedApps() const
{
    QStringList dirs;
    dirs << QStringLiteral("/usr/share/applications");
    const QString homeLocal = QStandardPaths::writableLocation(QStandardPaths::ApplicationsLocation);
    if (!homeLocal.isEmpty())
        dirs << homeLocal;
    const QString apkd = QDir::homePath() + QStringLiteral("/.local/share/applications");
    if (!dirs.contains(apkd))
        dirs << apkd;

    QVariantList apps;
    QSet<QString> seenPaths;

    for (const QString &dirPath : dirs) {
        const QDir dir(dirPath);
        if (!dir.exists())
            continue;
        const QFileInfoList files = dir.entryInfoList(
                    { QStringLiteral("*.desktop") }, QDir::Files, QDir::Name);
        for (const QFileInfo &fi : files) {
            const QString path = fi.absoluteFilePath();
            if (seenPaths.contains(path))
                continue;
            seenPaths.insert(path);

            if (desktopTruthy(readDesktopValue(path, QStringLiteral("NoDisplay")))
                    || desktopTruthy(readDesktopValue(path, QStringLiteral("Hidden")))) {
                continue;
            }
            const QString type = readDesktopValue(path, QStringLiteral("Type"));
            if (!type.isEmpty() && type != QLatin1String("Application"))
                continue;
            const QString exec = readDesktopValue(path, QStringLiteral("Exec"));
            if (exec.isEmpty())
                continue;
            if (path.contains(QStringLiteral("harbour-electric-eel")))
                continue;

            QString name = readDesktopValue(path, QStringLiteral("Name"));
            if (name.isEmpty())
                name = fi.completeBaseName();

            QVariantMap entry;
            entry.insert(QStringLiteral("name"), name);
            entry.insert(QStringLiteral("desktopFile"), path);
            entry.insert(QStringLiteral("icon"),
                         readDesktopValue(path, QStringLiteral("Icon")));
            apps.append(entry);
        }
    }

    std::sort(apps.begin(), apps.end(), [](const QVariant &a, const QVariant &b) {
        return a.toMap().value(QStringLiteral("name")).toString().localeAwareCompare(
                    b.toMap().value(QStringLiteral("name")).toString()) < 0;
    });
    return apps;
}
