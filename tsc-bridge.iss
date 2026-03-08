; TSC Bridge — InnoSetup Installer Script
; Compile with InnoSetup 6+ on Windows: iscc tsc-bridge.iss
; Or from macOS via Docker: docker run --rm -v $(pwd):/work amake/innosetup /work/tsc-bridge.iss

#define MyAppName "TSC Bridge"
#define MyAppVersion "3.0.0"
#define MyAppPublisher "Abstrakt GT"
#define MyAppURL "https://myprinter.com"
#define MyAppExeName "tsc-bridge.exe"

[Setup]
AppId={{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={localappdata}\tsc-bridge
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
OutputBaseFilename=TSC-Bridge-{#MyAppVersion}-Setup
SetupIconFile=tsc-bridge.ico
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName}
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=.

[Languages]
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
spanish.InstallingService=Instalando servicio TSC Bridge...
spanish.ConfiguringFirewall=Configurando reglas de firewall...
spanish.ConfiguringHosts=Configurando nombre de host local...
spanish.InstallingCertificate=Instalando certificado SSL...
spanish.StartingService=Iniciando servicio...
english.InstallingService=Installing TSC Bridge service...
english.ConfiguringFirewall=Configuring firewall rules...
english.ConfiguringHosts=Configuring local hostname...
english.InstallingCertificate=Installing SSL certificate...
english.StartingService=Starting service...

[Tasks]
Name: "desktopicon"; Description: "Crear acceso directo en el Escritorio"; GroupDescription: "Accesos directos:"
Name: "autostart"; Description: "Iniciar TSC Bridge con Windows"; GroupDescription: "Inicio automático:"; Flags: checkedonce

[Files]
Source: "tsc-bridge.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "tsc-bridge.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\tsc-bridge.ico"
Name: "{group}\Desinstalar {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\tsc-bridge.ico"; Tasks: desktopicon
Name: "{commonstartup}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Parameters: "--headless"; IconFilename: "{app}\tsc-bridge.ico"; Tasks: autostart

[Run]
; Start the service after install
Filename: "{app}\{#MyAppExeName}"; Description: "Iniciar TSC Bridge"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Stop service before uninstall
Filename: "taskkill"; Parameters: "/IM {#MyAppExeName} /F"; Flags: runhidden

[UninstallDelete]
Type: filesandordirs; Name: "{app}"

[Code]
const
  HOSTNAME = 'myprinter.com';

procedure ConfigureHostsFile();
var
  HostsPath: String;
  HostsContent: AnsiString;
begin
  HostsPath := ExpandConstant('{sys}\drivers\etc\hosts');
  if LoadStringFromFile(HostsPath, HostsContent) then
  begin
    if Pos(HOSTNAME, String(HostsContent)) = 0 then
    begin
      SaveStringToFile(HostsPath, #13#10 + '127.0.0.1  ' + HOSTNAME + #13#10, True);
      Log('Added ' + HOSTNAME + ' to hosts file');
    end
    else
      Log(HOSTNAME + ' already in hosts file');
  end;
end;

procedure ConfigureFirewall();
var
  ResultCode: Integer;
begin
  // Remove old rules
  Exec('netsh', 'advfirewall firewall delete rule name="TSC Bridge HTTP"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('netsh', 'advfirewall firewall delete rule name="TSC Bridge HTTPS"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  // Add new rules (default ports)
  Exec('netsh', 'advfirewall firewall add rule name="TSC Bridge HTTP" dir=in action=allow protocol=TCP localport=9638', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('netsh', 'advfirewall firewall add rule name="TSC Bridge HTTPS" dir=in action=allow protocol=TCP localport=9639', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  // Also allow custom port 9271/9272
  Exec('netsh', 'advfirewall firewall add rule name="TSC Bridge HTTP Alt" dir=in action=allow protocol=TCP localport=9271', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('netsh', 'advfirewall firewall add rule name="TSC Bridge HTTPS Alt" dir=in action=allow protocol=TCP localport=9272', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

procedure GenerateAndTrustCert();
var
  ResultCode: Integer;
  CertDir, CaPath: String;
begin
  CertDir := ExpandConstant('{userappdata}\tsc-bridge\certs');
  CaPath := CertDir + '\ca.pem';

  // Run bridge briefly to generate certs
  if not FileExists(CaPath) then
  begin
    Exec(ExpandConstant('{app}\{#MyAppExeName}'), '--headless', '', SW_HIDE, ewNoWait, ResultCode);
    Sleep(4000);
    Exec('taskkill', '/IM {#MyAppExeName} /F', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Sleep(1000);
  end;

  // Trust CA cert
  if FileExists(CaPath) then
  begin
    Exec('certutil', '-addstore -f "Root" "' + CaPath + '"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Log('CA certificate installed');
  end;
end;

procedure RemoveFirewallRules();
var
  ResultCode: Integer;
begin
  Exec('netsh', 'advfirewall firewall delete rule name="TSC Bridge HTTP"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('netsh', 'advfirewall firewall delete rule name="TSC Bridge HTTPS"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('netsh', 'advfirewall firewall delete rule name="TSC Bridge HTTP Alt"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('netsh', 'advfirewall firewall delete rule name="TSC Bridge HTTPS Alt"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    // Kill any running instance first
    WizardForm.StatusLabel.Caption := ExpandConstant('{cm:InstallingService}');

    // Configure hosts file
    WizardForm.StatusLabel.Caption := ExpandConstant('{cm:ConfiguringHosts}');
    ConfigureHostsFile();

    // Configure firewall
    WizardForm.StatusLabel.Caption := ExpandConstant('{cm:ConfiguringFirewall}');
    ConfigureFirewall();

    // Generate and trust SSL certificate
    WizardForm.StatusLabel.Caption := ExpandConstant('{cm:InstallingCertificate}');
    GenerateAndTrustCert();
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
  begin
    RemoveFirewallRules();
  end;
end;
