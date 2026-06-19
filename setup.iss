; Inno Setup script for CC Console
; Build: iscc /DMyAppVersion=1.3.2 setup.iss

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif
#define MyAppName "CC Console"
#define MyAppId "cc-console"
#define MyAppExe "cc-console.exe"

[Setup]
AppId={{F1A2B3C4-D5E6-7890-ABCD-EF1234567890}}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
DefaultDirName={localappdata}\cc-console
DefaultGroupName={#MyAppName}
OutputDir=.
OutputBaseFilename=cc-console-setup
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
ChangesAssociations=yes
ShowLanguageDialog=no

[Languages]
Name: "zh_CN"; MessagesFile: "Languages\ChineseSimplified.isl"
Name: "zh_TW"; MessagesFile: "Languages\ChineseTraditional.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
zh_CN.DesktopIcon=创建桌面快捷方式(&D)
zh_CN.Shortcuts=快捷方式:
zh_CN.LaunchApp=启动 {#MyAppName}
zh_TW.DesktopIcon=建立桌面捷徑(&D)
zh_TW.Shortcuts=捷徑:
zh_TW.LaunchApp=啟動 {#MyAppName}
en.DesktopIcon=Create a &desktop shortcut
en.Shortcuts=Shortcuts:
en.LaunchApp=Launch {#MyAppName}

[Tasks]
Name: "desktopicon"; Description: "{cm:DesktopIcon}"; GroupDescription: "{cm:Shortcuts}"

[Files]
Source: "cc-console.exe"; DestDir: "{app}"; Flags: ignoreversion restartreplace
Source: "cc-console-sl.exe"; DestDir: "{app}"; Flags: ignoreversion restartreplace
Source: "bridge.mjs"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExe}"; Tasks: desktopicon

[Code]
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
begin
  Result := '';

  // 尝试优雅关闭（WM_CLOSE）— 但 cc-console 隐藏到托盘，不退出
  Exec('taskkill', '/im ' + ExpandConstant('{#MyAppExe}'), '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  // 强制终止残留进程
  Exec('taskkill', '/f /im ' + ExpandConstant('{#MyAppExe}'), '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  // 短暂等待文件解锁
  Sleep(500);
end;

// 卸载时还原 ~/.claude/settings.json 的 statusLine(主 exe 此时仍在,由它执行还原)
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    Exec(ExpandConstant('{app}\{#MyAppExe}'), '--restore-statusline', '',
      SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

[Run]
Filename: "{app}\{#MyAppExe}"; Flags: nowait postinstall; Description: "{cm:LaunchApp}"
Filename: "{sys}\ie4uinit.exe"; Parameters: "-show"; Flags: runhidden skipifdoesntexist
