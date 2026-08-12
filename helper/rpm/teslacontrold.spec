# teslacontrold: privileged companion service for harbour-teslacontrol.
# NOT a Harbour package - ships a setcap'd binary and a system D-Bus
# service, neither of which Harbour review will host. Distribute via
# OpenRepos or install directly:
#   devel-su pkcon install-local teslacontrold-*.rpm
Name:       teslacontrold
Summary:    Privileged BLE helper service for harbour-teslacontrol
Version:    0.1.7
Release:    1
License:    ASL 2.0 and BSD
URL:        https://github.com/marconapetti/ElectricEel
Group:      Applications/System
Source0:    %{name}-%{version}.tar.gz
# Prebuilt, cross-compiled from the upstream teslamotors/vehicle-command
# Go module. Not built by this spec - see README.md for the Docker
# cross-compilation step. rpmbuild just packages the resulting binaries.
Source1:    teslacontrold
Source2:    tesla-control
Source3:    tesla-keygen
# Optional persistent-BLE-session companion (helper/session/) - only
# spawned when TESLACONTROLD_PERSISTENT_SESSION is set; see
# KNOWN_ISSUES.md. Bundled unconditionally like the other two so enabling
# the env var doesn't require a reinstall, but inert (never executed) with
# it unset.
Source4:    tesla-session
Source10:   teslacontrold.service
Source11:   org.teslacontrol.Helper.service
Source12:   org.teslacontrol.Helper.conf
Source13:   TeslaControlHelper.permission

BuildArch:  aarch64
ExclusiveArch: aarch64
# Prebuilt Go binaries carry no GNU build-id note, which trips up rpm's
# automatic debuginfo/find-debuginfo.sh extraction (it errors out looking
# for one) - there's no debug info to extract from them anyway.
%global debug_package %{nil}
%global _missing_build_ids_terminate_build 0
Requires:   systemd
Requires:   dbus
Requires(pre):  shadow-utils
Requires(post): /usr/sbin/setcap

%description
Runs a small system D-Bus service (org.teslacontrol.Helper) that wraps
Tesla's own tesla-control/tesla-keygen binaries with the CAP_NET_ADMIN
capability their BLE library needs. Exists because Sailjail's
Base.permission drops all capabilities from every sandboxed Harbour app
unconditionally, so the sandboxed harbour-teslacontrol UI has no way to
do this itself; it talks to this service over D-Bus instead.

%prep
# no source archive to unpack; everything comes from Source1-13

%build
# nothing to build - binaries are prebuilt, see README.md

%install
install -d %{buildroot}/opt/teslacontrold/bin
install -m 0755 %{SOURCE1} %{buildroot}/opt/teslacontrold/bin/teslacontrold
install -m 0755 %{SOURCE2} %{buildroot}/opt/teslacontrold/bin/tesla-control
install -m 0755 %{SOURCE3} %{buildroot}/opt/teslacontrold/bin/tesla-keygen
install -m 0755 %{SOURCE4} %{buildroot}/opt/teslacontrold/bin/tesla-session

install -d %{buildroot}%{_unitdir}
install -m 0644 %{SOURCE10} %{buildroot}%{_unitdir}/teslacontrold.service

install -d %{buildroot}%{_datadir}/dbus-1/system-services
install -m 0644 %{SOURCE11} %{buildroot}%{_datadir}/dbus-1/system-services/org.teslacontrol.Helper.service

install -d %{buildroot}%{_sysconfdir}/dbus-1/system.d
install -m 0644 %{SOURCE12} %{buildroot}%{_sysconfdir}/dbus-1/system.d/org.teslacontrol.Helper.conf

install -d %{buildroot}%{_sysconfdir}/sailjail/permissions
install -m 0644 %{SOURCE13} %{buildroot}%{_sysconfdir}/sailjail/permissions/TeslaControlHelper.permission

install -d %{buildroot}%{_localstatedir}/lib/teslacontrold

%pre
getent group teslacontrol >/dev/null || groupadd -r teslacontrol
getent passwd teslacontrol >/dev/null || \
    useradd -r -g teslacontrol -d /var/lib/teslacontrold -s /sbin/nologin \
        -c "teslacontrold service account" teslacontrol
exit 0

%post
# CAP_NET_ADMIN lets go-ble/ble bring the HCI adapter up/down without root;
# see the raw-HCI feasibility notes in README.md for why this is needed.
# tesla-session needs the same grant as tesla-control: with
# TESLACONTROLD_PERSISTENT_SESSION set, it's the one holding the BLE
# session, not tesla-control.
setcap 'cap_net_admin=eip' /opt/teslacontrold/bin/tesla-control
setcap 'cap_net_admin=eip' /opt/teslacontrold/bin/tesla-session
chown -R teslacontrol:teslacontrol /var/lib/teslacontrold
%systemd_post teslacontrold.service
systemctl daemon-reload >/dev/null 2>&1 || :
systemctl enable --now teslacontrold.service >/dev/null 2>&1 || :

%preun
%systemd_preun teslacontrold.service

%postun
%systemd_postun_with_restart teslacontrold.service

%files
%attr(0755,root,root) /opt/teslacontrold/bin/teslacontrold
%attr(0755,root,root) /opt/teslacontrold/bin/tesla-control
%attr(0755,root,root) /opt/teslacontrold/bin/tesla-keygen
%attr(0755,root,root) /opt/teslacontrold/bin/tesla-session
%{_unitdir}/teslacontrold.service
%{_datadir}/dbus-1/system-services/org.teslacontrol.Helper.service
%{_sysconfdir}/dbus-1/system.d/org.teslacontrol.Helper.conf
%{_sysconfdir}/sailjail/permissions/TeslaControlHelper.permission
%dir %attr(0700,teslacontrol,teslacontrol) %{_localstatedir}/lib/teslacontrold

%changelog
* Tue Aug 12 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.7-1
- Version bump
* Tue Aug 12 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.6-1
- Car model
* Tue Aug 12 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.5-1
- Version bump
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.4-1
- Add tesla-session, an optional persistent-BLE-session companion binary
  (off by default, TESLACONTROLD_PERSISTENT_SESSION) - not yet verified
  on-device, see KNOWN_ISSUES.md.
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.3-1
- Bump version
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.2-1
- Drop ProtectSystem=/ProtectHome=/PrivateTmp=/ProtectKernelTunables=/
  ProtectControlGroups=/RestrictNamespaces=/SystemCallFilter= from
  teslacontrold.service: on the phone's systemd 238 these forced
  NoNewPrivileges (kernel PR_SET_NO_NEW_PRIVS) on regardless of the
  unit's explicit NoNewPrivileges=false, silently breaking the setcap'd
  tesla-control child's CAP_NET_ADMIN grant and making pairing fail with
  a misleading "operation not permitted" BLE adapter error.
* Tue Aug 11 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.1-1
- Rewrite helper in Rust
* Fri Aug 07 2026 Marco Napetti <marco.napetti@proton.me> - 0.1.0-1
- Initial packaging.
