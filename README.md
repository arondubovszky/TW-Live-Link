# TW Live Link

TW Live Link is a small Windows app for **Team Wars** (a Forza Horizon 6 team league). It takes the
telemetry that Forza sends through its **Data Out** setting and passes it on to the Team Wars live
server, so your car appears in the live race view during Team Wars races.

This repository contains the complete source code of the app and its installer, so anyone can check
what it does. The installer is also built from this code by GitHub itself (see
[Check that the installer is built from this code](#check-that-the-installer-is-built-from-this-code)).

The code is shared for review only: see [LICENSE](LICENSE).

## What it does

1. **Forza → the app.** You set Forza's Data Out to `127.0.0.1`, port `8001`. Forza then sends its
   telemetry packets (position, speed, gear, inputs, lap times and so on, about 60 a second) to the
   app on your own PC.
2. **The app → the live server, only during a race.** While race control has a Team Wars race open,
   the app passes those packets on, unchanged, to `twlive.duckdns.org`. Outside races it sends no
   telemetry at all.
3. **Optionally → your own telemetry apps.** If you use something like SimHub, the app can pass the
   same packets on to it on your own PC, so it keeps working. This is off until you switch it on per app.

## Exactly what it sends, and where

The app only connects to the Team Wars live server, `https://twlive.duckdns.org`
(`DefaultServer` in [core.go](core.go)):

| Address | When | What is sent |
|---|---|---|
| `/live/link` | every 5 s while Forza is running or the window is open, otherwise once a minute | the team and name you picked; the answer says whether a race is open |
| `/live/ingest` (WebSocket) | only while a race is open | Forza's telemetry packets, unchanged, plus how long each one waited in the app |
| `/live/roster`, `/live/teams.js` | when you pick your team and name | nothing; it downloads the team list, logos and colours |
| `/live/app-version` | at start, while the window is open (once a minute) and otherwise hourly | nothing; it asks for the newest version number |

It does **not** read or send files, passwords, keystrokes, screenshots, your other programs, your
Windows account or anything else. There is no tracking or analytics.

## What it does on your PC

- **Listens** for Forza's packets on UDP port `8001` (you can change the port if another app uses it).
- **Shows its window** from a small web page served only to your own PC (`127.0.0.1:17771`), opened in
  Microsoft Edge's app mode (or your normal browser if Edge isn't installed).
- **Saves your settings** (team, name, port, which apps to pass data to) in
  `%APPDATA%\TW Live Link\settings.json`.
- **Start with Windows** (a choice in the installer, ticked by default): one entry under
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`. You can switch it off at any time in the app's settings.
- **Looks for telemetry apps** you may use (like SimHub), only while Forza is running, every 30 s:
  it runs Windows' own `netstat` and `tasklist` to see which programs are listening for telemetry, so it
  can offer to pass data on to them. Nothing is sent anywhere for this.

The installer ([installer.nsi](installer.nsi)):

- puts the app in `C:\Program Files\TW Live Link`, adds a Start menu shortcut (and a desktop shortcut
  if you tick it), and registers it under *Apps* so you can uninstall it normally;
- closes an older copy of the app if one is running, so it can be replaced;
- for the **Microsoft Store / Xbox app version of Forza** only: allows Forza to send its Data Out to your
  own PC (`127.0.0.1`). Store apps aren't allowed to do that by default; it uses Windows' own
  `CheckNetIsolation LoopbackExempt` command for the installed Forza package. The Steam version doesn't
  need this, and it changes nothing for other apps.

That is why the installer asks for permission once.

## Updates

The app checks `/live/app-version` for a newer version. If there is one, it shows an **Update** button.
Only when you click it does it download the new installer from the same server, run it silently
(Windows asks for permission), and start the new version. It never updates without your click.

## Check that the installer is built from this code

The build is **reproducible**: the same code always gives exactly the same app and the same installer,
byte for byte, wherever it's built. So you can check that the installer people download is built from
exactly this code.

Every change to this repository is built by GitHub on its own machines, using
[.github/workflows/build.yml](.github/workflows/build.yml):

1. Open the **Actions** tab and pick the latest **Build TW Live Link** run.
2. The run's summary shows the **SHA-256 fingerprints** of the app and the installer it built, and the
   files are attached under **Artifacts**.
3. Compare them with the files on your PC, in PowerShell:

   ```
   Get-FileHash .\TW-Live-Link-Setup.exe -Algorithm SHA256
   Get-FileHash "C:\Program Files\TW Live Link\TW Live Link.exe" -Algorithm SHA256
   ```

   The first is the installer you downloaded, the second the app as installed on your PC. Both should
   show the same fingerprint as the GitHub build of that version.

You can also build it yourself, exactly like the workflow does (on Ubuntu 24.04 with Go 1.23,
`binutils-mingw-w64-x86-64` and `nsis`):

```
(cd res && x86_64-w64-mingw32-windres --preprocessor=cat -O coff -o ../rsrc_windows_amd64.syso app.rc)
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w" -o "build/TW Live Link.exe" .
makensis -V1 installer.nsi
```

The app uses only Go's standard library: no outside packages.

## Files

| File | What's in it |
|---|---|
| [main.go](main.go) | start-up: one copy at a time, the tray icon, the window |
| [core.go](core.go) | receiving Forza's packets, the connection to the live server, settings, updates |
| [ui.go](ui.go) | the small local web page behind the window |
| [ui/index.html](ui/index.html) | the window itself |
| [sys_windows.go](sys_windows.go) | Windows parts: tray icon, window, Start with Windows, finding telemetry apps |
| [netstat.go](netstat.go) | reading Windows' `netstat` output |
| [installer.nsi](installer.nsi) | the installer |
| [res/](res/) | icons, version information and installer images |
