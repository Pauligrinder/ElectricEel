#ifndef UISETTINGS_H
#define UISETTINGS_H

#include <QStandardPaths>
#include <QString>

// Sailjail only allows writes under the app's config/data dirs
// (~/.config/org.electriceel/harbour-electric-eel/ and the matching
// share path). QSettings(org, app) targets
// ~/.config/org.electriceel/harbour-electric-eel.conf — a sibling *file*
// firejail blocks — so toggles never persisted. Keep UI prefs next to
// config.json instead.
inline QString uiSettingsPath()
{
    return QStandardPaths::writableLocation(QStandardPaths::AppDataLocation)
            + QStringLiteral("/ui-settings.ini");
}

#endif // UISETTINGS_H
