Name:       harbour-teslacontrol
Summary:    Control your Tesla over Bluetooth
Version:    0.1.2
Release:    1
License:    ASL 2.0
URL:        https://github.com/marconapetti/ElectricEel
Source0:    %{name}-%{version}.tar.bz2
Requires:   sailfishsilica-qt5 >= 0.10.9
Requires:   qt5-qtcore
Requires:   qt5-qtdeclarative
BuildRequires:  pkgconfig(sailfishapp) >= 1.0.2
BuildRequires:  pkgconfig(Qt5Core)
BuildRequires:  pkgconfig(Qt5Qml)
BuildRequires:  pkgconfig(Qt5Quick)
BuildRequires:  pkgconfig(Qt5DBus)
BuildRequires:  desktop-file-utils

%description
Sandboxed Silica UI for tesla-control (teslamotors/vehicle-command), grouped
like the official Tesla app: quick actions, climate, charging, locks &
security, trunk/frunk/windows, media, software, keys and diagnostics. All
BLE work needing CAP_NET_ADMIN happens out-of-sandbox in the companion
teslacontrold service, which this app talks to over D-Bus - see the
teslacontrold package for that half.

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
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.2-1
- Bump version
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.1-1
- Rewrite helper in Rust
* Fri Aug 07 2026 Marco Napetti <marco.napetti@firma.ai> - 0.1.0-1
- Initial packaging.
