Name:       harbour-electric-eel
Summary:    Control your Tesla over Bluetooth
Version:    0.2.26
Release:    1
License:    ASL 2.0
URL:        https://github.com/nappa85/ElectricEel
Source0:    %{name}-%{version}.tar.bz2
Requires:   sailfishsilica-qt5 >= 0.10.9
Requires:   qt5-qtcore
Requires:   qt5-qtdeclarative
Requires:   qt5-qtdbus
BuildRequires:  pkgconfig(sailfishapp) >= 1.0.2
BuildRequires:  pkgconfig(Qt5Core)
BuildRequires:  pkgconfig(Qt5Qml)
BuildRequires:  pkgconfig(Qt5Quick)
BuildRequires:  pkgconfig(Qt5DBus)
BuildRequires:  desktop-file-utils

%description
Sandboxed Silica UI for controlling a Tesla over Bluetooth, grouped like
the official Tesla app: quick actions, climate, charging, locks & security,
trunk/frunk/windows, media, software, keys and diagnostics. BLE transport
runs through a cooperative org.bluez backend (no raw HCI takeover), driven
by an in-process Rust control core (staticlib) that spawns the bundled
tesla-session binary. No privileged helper service, no CAP_NET_ADMIN, no
devel-su - a single self-contained Harbour app.

%prep
%setup -q -n %{name}-%{version}

%build
%qmake5
make %{?_smp_mflags}

%install
rm -rf %{buildroot}
%qmake5_install

desktop-file-install --delete-original \
  --dir %{buildroot}%{_datadir}/applications \
   %{buildroot}%{_datadir}/applications/*.desktop

%files
%defattr(-,root,root,-)
%{_bindir}/%{name}
%{_datadir}/%{name}
%{_datadir}/applications/%{name}.desktop
%{_datadir}/icons/hicolor/*/apps/%{name}.png

%changelog
* Tue Sep 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.26-1
- Drop drive-app background toggle and test-launch button

* Tue Sep 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.25-1
- Background drive-app launch: reclaim ElectricEel focus after start (Lipstick always raises)

* Tue Sep 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.24-1
- Launch drive apps via systemd D-Bus StartTransientUnit (Sailjail blocks systemd-run exec)

* Tue Sep 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.23-1
- Persist hotspot/drive-app UI settings under Sailjail AppData (not blocked .conf path)
- Launch drive app via systemd --user scope (AppLaunch); test launch always foreground

* Tue Sep 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.22-1
- Launch chosen app when driving; test launch button in Settings

* Sat Sep 26 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.21-1
- Wi-Fi hotspot when the phone is inside the car (very strong RSSI); off after walk-away if we turned it on
- Wake the Bluetooth-off watcher as soon as BlueZ reports the adapter powered
* Fri Sep 25 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.20-1
- Do not recycle discovery when the car is away; forget a frozen leftover Device1 only once
- After a short GATT bounce, drop the -93 reconnect slack so a last-gasp Connect cannot hang
* Fri Sep 25 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.19-1
- Recycle dead LE discovery and drop a frozen leftover Device1 so attach does not need a Bluetooth restart
* Sun Sep 20 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.18-1
- Merge nappa i18n, share destination, and Unix-socket tesla-session IPC
- Own process group so covering the phone does not SIGTERM a live GATT link
- Wait 2s after GATT teardown before restarting LE discovery
* Sun Sep 20 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.11-1
- Share target registration: X-Share-Methods belongs in [Desktop Entry]
- Generic map-URL parsing (no per-service list)
- Parent/child transport over Unix socket with versioned handshake
* Sun Sep 20 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.10-1
- Share destination to car navigation over BLE (field-53 coordinates, field-21 address)

* Sun Sep 13 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.17-1
- Connect phone-key to a live BlueZ advertisement instead of RemoveDevice
- Quiet period, -95 RSSI floor, and negotiated MTU after GATT drop (Tesla Android)
- Stop dashboard refresh from racing phone-key connect and wedging bluetoothd
* Sat Aug 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.16-1
- Ignore stale BlueZ disconnects so phone-key does not flap after auth
* Sat Aug 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.15-1
- Keep phone-key GATT up: do not handshake infotainment while presence is active
* Sat Aug 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.14-1
- Keep phone-key VCSEC session when dashboard state times out on a sleeping car
- Cover actions: cycle lock/trunk/frunk/climate/charge port, then run
* Sat Aug 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.13-1
- Stop GATT connect timeouts on stale BlueZ RSSI and live discovery
* Sat Aug 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.12-1
- Fix signal-driven phone-key scanning (shared D-Bus channel and advertisement parsing)
- Fix the Rust CI gate (formatting and pedantic clippy lints)
* Sat Aug 29 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.11-1
- Drive phone-key scanning from BlueZ signals to save battery
* Wed Aug 26 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.10-1
- Stop presence-loop D-Bus spin on cached BlueZ devices
* Wed Aug 26 2026 Pauli Kettunen <pauligrinder@gmail.com> - 0.2.9-1
- Stabilize phone-key GATT reconnect and VCSEC presence

* Thu Aug 22 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.8-1
- Restart session on wakeup
* Thu Aug 22 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.7-1
- Enable BlueZ AutoConnect
* Thu Aug 22 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.6-1
- Ensure child process is killed with parent
* Thu Aug 18 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.5-1
- App cover shows connection status
- Phone as key
* Thu Aug 18 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.4-1
- Fix sliders
* Thu Aug 15 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.3-1
- Busy indicator in config pages
* Thu Aug 14 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.2-1
- Keyless drive
* Thu Aug 13 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.1-1
- Proximity unlock
* Thu Aug 13 2026 Marco Napetti <marco.napetti@proton.me> - 0.2.0-1
- Renamed the app from harbour-teslacontrol to harbour-electric-eel (ElectricEel)
- BlueZ D-Bus BLE backend; in-process Rust core, no privileged helper service
* Tue Aug 12 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.7-1
- Avoid config edit before config load
* Tue Aug 12 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.6-1
- Car model
* Tue Aug 12 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.5-1
- UI improvements
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.4-1
- Version bump
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.3-1
- UI revamped, thanks to @cypherpunks
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.2-1
- Bump version
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.1-1
- Rewrite helper in Rust
* Fri Aug 07 2026 Marco Napetti <marco.napetti@firma.ai> - 0.1.0-1
- Initial packaging.
