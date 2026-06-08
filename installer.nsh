; SyncSave NSIS custom include.
; Electron Builder owns the installer pages; this file adds Windows Firewall
; rules so installed builds can accept LAN discovery, direct P2P HTTP, and
; optional self-hosted relay traffic on ALL network profiles (public, private, domain).

!macro customInstall
  DetailPrint "Configuring Windows Firewall rules for SyncSave..."
  ; Remove any old rules first to avoid duplicates
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync TCP Inbound"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync UDP Discovery"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync TCP"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync UDP"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SyncSave TCP"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SyncSave UDP"'

  ; TCP rule — covers dashboard (8383), relay (8386), all profiles
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall add rule name="SyncSave TCP" dir=in action=allow protocol=TCP localport=8383-8387 profile=any enable=yes description="SyncSave P2P sync and relay TCP traffic"'
  ; UDP rule — covers LAN discovery broadcast (8385), all profiles
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall add rule name="SyncSave UDP" dir=in action=allow protocol=UDP localport=8383-8387 profile=any enable=yes description="SyncSave LAN peer discovery UDP broadcast"'
!macroend

!macro customUnInstall
  DetailPrint "Removing Windows Firewall rules for SyncSave..."
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SyncSave TCP"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SyncSave UDP"'
  ; Also clean up old rule names for good measure
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync TCP Inbound"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync UDP Discovery"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync TCP"'
  nsExec::ExecToLog '"$SYSDIR\netsh.exe" advfirewall firewall delete rule name="SaveSync UDP"'
!macroend
