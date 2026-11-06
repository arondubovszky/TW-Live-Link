; TW Live Link installer
Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"

!define APPNAME "TW Live Link"
!define VERSION "1.5.0"
!define PUBLISHER "Team Wars"
!define EXE "TW Live Link.exe"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\TWLiveLink"

Name "${APPNAME}"
OutFile "build\TW-Live-Link-Setup.exe"
; files are stored without their date and time, so the installer depends only on the code (the same code always
; gives exactly the same installer, wherever it is built)
SetDateSave off
InstallDir "$PROGRAMFILES64\${APPNAME}"
InstallDirRegKey HKLM "${UNINSTKEY}" "InstallLocation"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
BrandingText "Team Wars Live"
VIProductVersion "1.5.0.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "FileDescription" "${APPNAME} setup"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "${PUBLISHER}"

!define MUI_ICON "res\app.ico"
!define MUI_UNICON "res\app.ico"
ManifestDPIAware true          ; sharp text on high-DPI screens
!define MUI_WELCOMEFINISHPAGE_BITMAP "res\side.bmp"
!define MUI_WELCOMEFINISHPAGE_BITMAP_STRETCH "FitControl"   ; the images are made at twice the size, shrunk to fit
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "res\side.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP_STRETCH "FitControl"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_BITMAP "res\header.bmp"
!define MUI_HEADERIMAGE_BITMAP_STRETCH "FitControl"
!define MUI_HEADERIMAGE_RIGHT
!define MUI_ABORTWARNING

!define MUI_WELCOMEPAGE_TITLE "TW Live Link"
!define MUI_WELCOMEPAGE_TEXT "Shows your car in the Team Wars live view while you race.$\r$\n$\r$\nAfter installing: pick your team and name once, and set Forza's Data Out to 127.0.0.1, port 8001. The Live Link shows exactly what to set.$\r$\n$\r$\nYour other telemetry apps (SimHub and the like) keep working: the Live Link passes the data on to them."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Open TW Live Link now"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "TW Live Link" SecMain
  SectionIn RO
  ; an older copy may be running: stop it so it can be replaced
  nsExec::Exec 'taskkill /IM "${EXE}" /F'
  Sleep 500
  SetOutPath "$INSTDIR"
  File "build\${EXE}"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortCut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\${EXE}" 0
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\${EXE}"
  WriteRegStr HKLM "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1
  ; the Microsoft Store / Xbox app version of Forza may only send Data Out to this PC (127.0.0.1) after this
  ; (the Steam version doesn't need it; it's harmless there)
  nsExec::Exec 'powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-AppxPackage -AllUsers *Forza* | ForEach-Object { CheckNetIsolation LoopbackExempt -a -n=$$($$_.PackageFamilyName) }"'
SectionEnd

Section "Desktop shortcut" SecDesk
  IfSilent +2   ; an update (silent) keeps the player's own choices
  CreateShortCut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\${EXE}" 0
SectionEnd

Section "Start with Windows (recommended)" SecAuto
  IfSilent autodone
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPNAME}" '"$INSTDIR\${EXE}" --background'
  ; and tell the app, so its own switch shows it on
  CreateDirectory "$APPDATA\TW Live Link"
  IfFileExists "$APPDATA\TW Live Link\settings.json" +2 0
  FileOpen $0 "$APPDATA\TW Live Link\settings.json" w
  StrCmp $0 "" +3 0
  FileWrite $0 '{"server":"https://twlive.duckdns.org","listenPort":8001,"forward":{},"autoStart":true}'
  FileClose $0
  autodone:
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} "The Live Link app."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesk} "An icon on your desktop."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecAuto} "Starts quietly in the tray when Windows starts, so you're always ready for a race."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  nsExec::Exec 'taskkill /IM "${EXE}" /F'
  Sleep 500
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APPNAME}.lnk"
  Delete "$DESKTOP\${APPNAME}.lnk"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPNAME}"
  DeleteRegKey HKLM "${UNINSTKEY}"
SectionEnd
