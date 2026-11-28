Unicode true
RequestExecutionLevel user
ManifestDPIAware true
Name "Nexus"
OutFile "{{out_file}}"
Icon "{{installer_icon}}"
SetCompressor /SOLID lzma
AutoCloseWindow true
VIProductVersion "{{version_with_build}}"
VIAddVersionKey "ProductName" "Nexus"
VIAddVersionKey "FileVersion" "{{version}}"
VIAddVersionKey "FileDescription" "Nexus Windows x64 Setup"
VIAddVersionKey "LegalCopyright" "Nexus contributors"
!include LogicLib.nsh
!include FileFunc.nsh
!include x64.nsh
!include Win\RestartManager.nsh
!define Marker "Software\Nexus\DesktopInstaller"
!define ARP "Software\Microsoft\Windows\CurrentVersion\Uninstall\NexusDesktop"
!define WV "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
Page instfiles
UninstPage instfiles
Var OldVersion
Var Prev
Var Mutex
Var Maintenance
Var ReportFailure
Var Ticket

!macro Fail text
  ${If} $ReportFailure != ""
    WriteRegStr HKLM "${Marker}" "UninstallFailed" "$ReportFailure"
  ${EndIf}
  SetErrorLevel 1
  MessageBox MB_OK|MB_ICONSTOP "${text}" /SD IDOK
  Abort
!macroend
!macro Run program args fail
  ClearErrors
  ExecWait '"${program}" ${args}' $0
  IfErrors ${fail}
  StrCmp $0 0 +2
    Goto ${fail}
!macroend
!macro Begin prefix
  ${IfNot} ${RunningX64}
    !insertmacro Fail "需要 Windows x64。"
  ${EndIf}
  SetRegView 64
  SetShellVarContext all
  StrCpy $INSTDIR "$PROGRAMFILES64\Nexus"
  ${GetParameters} $2
  UserInfo::GetAccountType
  Pop $3
  !if "${prefix}" == "un."
    ${If} $3 == "Admin"
      ${GetOptions} $2 "/Ticket=" $ReportFailure
    ${EndIf}
  !endif
  ReadRegStr $0 HKLM "${Marker}" "Location"
  ${If} ${FileExists} "$INSTDIR"
  ${AndIf} $0 != "$INSTDIR"
    !insertmacro Fail "安装目录已存在且不属于 Nexus。"
  ${EndIf}
  ; Only the bundler injects this compile-time literal. No user-supplied script or paths.
  System::Call 'kernel32::SetEnvironmentVariableW(w "PSModulePath",w "$SYSDIR\WindowsPowerShell\v1.0\Modules")'
  @CHECK_ACL_ENV@
  !insertmacro Run "$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" '-NoLogo -NoProfile -NonInteractive -Command "& ([ScriptBlock]::Create([Text.Encoding]::Unicode.GetString([Convert]::FromBase64String(@CHECK_ACL_EXPR@)))) 0"' unsafe_dir
  ${If} $3 == "Admin"
    ClearErrors
    ${GetOptions} $2 "/NexusMachine" $1
    ${If} ${Errors}
      !insertmacro Fail "请从普通用户会话打开安装程序，以便恢复该用户的网络设置。"
    ${EndIf}
    System::Call 'kernel32::CreateMutexW(p 0,i 1,w "Global\NexusDesktopMaintenance-fa53d024") p .r4 ?e'
    Pop $5
    ${If} $4 == 0
    ${OrIf} $5 == 183
      !insertmacro Fail "已有另一项 Nexus 安装操作在运行。"
    ${EndIf}
    StrCpy $Mutex $4
    ; A split-admin account can have a user SID as its default object owner.
    ; Make every newly created installation object owned by Administrators.
    System::Call 'advapi32::ConvertStringSidToSidW(w "S-1-5-32-544",*p .r5)i.r0'
    StrCmp $0 0 unsafe_dir
    System::Call 'advapi32::OpenProcessToken(p -1,i 0x80,*p .r6)i.r0'
    StrCmp $0 0 unsafe_dir
    System::Call 'advapi32::SetTokenInformation(p r6,i 4,*p r5,i ${NSIS_PTR_SIZE})i.r0'
    System::Call 'kernel32::CloseHandle(p r6)'
    System::Call 'kernel32::LocalFree(p r5)'
    StrCmp $0 0 unsafe_dir
    Return
  ${EndIf}
  ; --cleanup runs under the original account, including over-the-shoulder UAC.
  ${If} ${FileExists} "$INSTDIR\app\nexus-desktop.exe"
    !insertmacro Run "$INSTDIR\app\nexus-desktop.exe" "--cleanup" cleanup_failed
  ${EndIf}
  StrCpy $1 "/NexusMachine"
  ClearErrors
  ${GetOptions} $2 "/P" $0
  ${IfNot} ${Errors}
    StrCpy $1 "/S /NexusMachine"
  ${EndIf}
  IfSilent 0 +2
    StrCpy $1 "/S /NexusMachine"
  ClearErrors
  !if "${prefix}" == "un."
    System::Call 'kernel32::GetCurrentProcessId()i.r4'
    System::Call 'kernel32::GetTickCount64()l.r5'
    StrCpy $Ticket "$4-$5"
    ExecShellWait "runas" "$INSTDIR\app\uninstall.exe" "$1 /Ticket=$Ticket" SW_SHOWNORMAL $0
  !else
    ExecShellWait "runas" "$EXEPATH" "$1" SW_SHOWNORMAL $0
  !endif
  IfErrors permission_failed
  !if "${prefix}" == "un."
    ; NSIS's self-copy launcher exits before its actual elevated worker.
    ; Success requires the protected registration AND installed image to disappear.
    ${If} $0 == 0
      StrCpy $4 900
      await_worker:
        ReadRegStr $5 HKLM "${Marker}" "UninstallFailed"
        StrCmp $5 "$Ticket" cleanup_failed
        ReadRegStr $5 HKLM "${Marker}" "Location"
        ${If} $5 == ""
        ${AndIfNot} ${FileExists} "$INSTDIR\app\nexus-desktop.exe"
          Goto worker_complete
        ${EndIf}
        Sleep 100
        IntOp $4 $4 - 1
        IntCmp $4 0 cleanup_failed cleanup_failed await_worker
      worker_complete:
    ${EndIf}
  !endif
  !if "${prefix}" == ""
    ${If} $0 == 0
      ClearErrors
      ${GetOptions} $2 "/UPDATE" $3
      ${IfNot} ${Errors}
        Exec '"$INSTDIR\app\nexus-desktop.exe"'
      ${EndIf}
      StrCpy $0 0
    ${EndIf}
  !endif
  SetErrorLevel $0
  Quit
  unsafe_dir:
    !insertmacro Fail "请检查安装目录权限，并让有待恢复代理配置的用户登录 Nexus 后退出。"
  permission_failed:
    !insertmacro Fail "安装或卸载需要管理员权限。"
  cleanup_failed:
    !insertmacro Fail "请先断开并退出 Nexus，然后重试。当前用户的恢复尚未完成。"
!macroend
Function .onInit
  !insertmacro Begin ""
FunctionEnd

; Shell/core hold shared read handles on this installation-wide file. Keeping
; an exclusive handle closes the gap between current-user cleanup and UAC.
!macro LockMaintenance
  System::Call 'kernel32::CreateFileW(w "$INSTDIR\maintenance.lock",i 0x80000000,i 0,p 0,i 4,i 0x00200080,p 0)p.r9'
  StrCmp $9 -1 busy
  StrCpy $Maintenance $9
  !insertmacro Run "$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" '-NoLogo -NoProfile -NonInteractive -Command "& ([ScriptBlock]::Create([Text.Encoding]::Unicode.GetString([Convert]::FromBase64String(@CHECK_ACL_EXPR@)))) 1"' outstanding
!macroend
Function un.onInit
  !insertmacro Begin "un."
FunctionEnd

!macro CheckClosed file
  ${If} ${FileExists} "${file}"
    !insertmacro RestartManager_StartSession $R0
    StrCmp $R0 "" busy
    !insertmacro RestartManager_RegisterFile $R0 "${file}"
    StrCmp $0 0 +3
      System::Call 'rstrtmgr::RmEndSession(i R0)'
      Goto busy
    System::Call 'rstrtmgr::RmGetList(i R0,*i .r1,*i 0,p 0,*i .r2)i.r0'
    System::Call 'rstrtmgr::RmEndSession(i R0)'
    StrCmp $0 0 +2
      Goto busy
  ${EndIf}
!macroend

Section "Nexus"
  StrCpy $Prev "$INSTDIR\previous"
  ReadRegStr $OldVersion HKLM "${ARP}" "DisplayVersion"
  ClearErrors
  WriteRegStr HKLM "${Marker}" "Location" "$INSTDIR"
  CreateDirectory "$INSTDIR"
  IfErrors failed
  !insertmacro LockMaintenance
  !insertmacro CheckClosed "$INSTDIR\app\nexus-desktop.exe"
  !insertmacro CheckClosed "$INSTDIR\app\nexus-core.exe"
  ; An interrupted directory swap is reversible on the next setup.
  IfFileExists "$Prev\nexus-desktop.exe" 0 stage
  ReadRegStr $0 HKLM "${Marker}" "Swapping"
  StrCmp $0 "yes" 0 clear_previous
  ${If} ${FileExists} "$INSTDIR\app\nexus-tun-helper.exe"
    !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--remove-service" failed
    RMDir /r "$INSTDIR\app"
  ${EndIf}
  ClearErrors
  Rename "$Prev" "$INSTDIR\app"
  IfErrors failed
  !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--install-service" failed
  Goto stage
  clear_previous:
    RMDir /r "$Prev"
  stage:
  SetOutPath "$INSTDIR"
  RMDir /r "$INSTDIR\next"
  IfFileExists "$INSTDIR\next" failed
  ClearErrors
  SetOutPath "$INSTDIR\next"
  File "/oname=nexus-desktop.exe" "{{main_binary_path}}"
  {{#each resources_dirs}}
  CreateDirectory "$OUTDIR\\{{this}}"
  {{/each}}
  {{#each resources}}
  File "/oname={{this.[1]}}" "{{no-escape @key}}"
  {{/each}}
  WriteUninstaller "$OUTDIR\uninstall.exe"
  IfErrors failed
  SetRegView 32
  ReadRegStr $0 HKLM "${WV}" "pv"
  SetRegView 64
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    File "/oname=WebView2Setup.exe" "{{webview2_bootstrapper_path}}"
    !insertmacro Run "$OUTDIR\WebView2Setup.exe" "/silent /install" failed
    Delete "$OUTDIR\WebView2Setup.exe"
  ${EndIf}
  SetOutPath "$INSTDIR"
  ClearErrors
  WriteRegStr HKLM "${Marker}" "Swapping" "yes"
  IfErrors failed
  ${If} ${FileExists} "$INSTDIR\app\nexus-tun-helper.exe"
    !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--remove-service" failed
    ClearErrors
    Rename "$INSTDIR\app" "$Prev"
    IfErrors restore_service
  ${EndIf}
  ClearErrors
  Rename "$INSTDIR\next" "$INSTDIR\app"
  IfErrors rollback
  !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--install-service" discard_new
  ClearErrors
  CreateShortcut "$SMPROGRAMS\Nexus.lnk" "$INSTDIR\app\nexus-desktop.exe"
  WriteRegStr HKLM "${ARP}" "DisplayName" "Nexus"
  WriteRegStr HKLM "${ARP}" "DisplayIcon" "$INSTDIR\app\nexus-desktop.exe"
  WriteRegStr HKLM "${ARP}" "DisplayVersion" "{{version}}"
  WriteRegStr HKLM "${ARP}" "UninstallString" '"$INSTDIR\app\uninstall.exe"'
  IfErrors undo_new
  WriteRegStr HKLM "${Marker}" "Swapping" ""
  IfErrors undo_new
  RMDir /r "$Prev"
  Goto done
  undo_new:
    !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--remove-service" failed
  discard_new:
    RMDir /r "$INSTDIR\app"
  rollback:
    ${If} ${FileExists} "$Prev\nexus-tun-helper.exe"
      ClearErrors
      Rename "$Prev" "$INSTDIR\app"
      IfErrors failed
      WriteRegStr HKLM "${ARP}" "DisplayVersion" "$OldVersion"
      Goto restore_service
    ${EndIf}
    Delete "$SMPROGRAMS\Nexus.lnk"
    DeleteRegKey HKLM "${ARP}"
    Goto failed
  restore_service:
    !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--install-service" failed
  failed:
    !insertmacro Fail "安装未完成。请关闭 Nexus 后使用同一安装程序重试恢复。"
  busy:
    !insertmacro Fail "另一个会话正在使用 Nexus，请先退出。"
  outstanding:
    !insertmacro Fail "请让有待恢复代理配置的用户登录 Nexus 并退出，然后重试。"
  done:
SectionEnd

Section "Uninstall"
  !insertmacro LockMaintenance
  !insertmacro CheckClosed "$INSTDIR\app\nexus-desktop.exe"
  !insertmacro CheckClosed "$INSTDIR\app\nexus-core.exe"
  !insertmacro Run "$INSTDIR\app\nexus-tun-helper.exe" "--remove-service" failed
  SetOutPath "$PROGRAMFILES64"
  ClearErrors
  RMDir /r "$INSTDIR\previous"
  RMDir /r "$INSTDIR\next"
  RMDir /r "$INSTDIR\app"
  IfErrors failed
  System::Call 'kernel32::CloseHandle(p $Maintenance)'
  Delete "$INSTDIR\maintenance.lock"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\Nexus.lnk"
  DeleteRegKey HKLM "${ARP}"
  DeleteRegKey HKLM "${Marker}"
  Goto done
  busy:
  failed:
    !insertmacro Fail "卸载未完成。请先退出所有会话中的 Nexus，再重试。"
  outstanding:
    !insertmacro Fail "请让有待恢复代理配置的用户登录 Nexus 并退出，然后重试。"
  done:
SectionEnd
