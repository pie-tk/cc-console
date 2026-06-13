; Inno Setup 脚本 — Claude Code Monitor
; 编译: iscc /DMyAppVersion=1.2.7 setup.iss

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif
#define MyAppName "Claude Code 监控"
#define MyAppId "claude-code-monitor"
#define MyAppExe "claude-monitor.exe"

[Setup]
AppId={{F1A2B3C4-D5E6-7890-ABCD-EF1234567890}}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
DefaultDirName={localappdata}\claude-code-monitor
DefaultGroupName={#MyAppName}
OutputDir=.
OutputBaseFilename=claude-monitor-setup
SetupIconFile=icon.ico
UninstallDisplayIcon={app}\{#MyAppExe}
CloseApplications=yes
CloseApplicationsFilter=*.exe
PrivilegesRequired=lowest
WizardStyle=modern
Compression=lzma2
SolidCompression=yes
DisableWelcomePage=yes
DisableProgramGroupPage=yes

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "快捷方式:"

[Files]
Source: "claude-monitor.exe"; DestDir: "{app}"; Flags: ignoreversion restartreplace

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"; Tasks: desktopicon

[Code]
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  KillCount: Integer;
begin
  Result := '';
  KillCount := 0;

  // 尝试优雅关闭（WM_CLOSE）— 但 claude-monitor 隐藏到托盘，不退出
  Exec('taskkill', '/im ' + ExpandConstant('{#MyAppExe}'), '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  // 强制终止残留进程
  Exec('taskkill', '/f /im ' + ExpandConstant('{#MyAppExe}'), '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  // 短暂等待文件解锁
  Sleep(500);
end;

[Run]
Filename: "{app}\{#MyAppExe}"; Flags: nowait postinstall; Description: "启动 {#MyAppName}"
Filename: "ie4uinit.exe"; Parameters: "-show"; Flags: runhidden
