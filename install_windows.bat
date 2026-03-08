@echo off
setlocal EnableDelayedExpansion

:: ============================================================
::  TSC Bridge — Instalador desatendido para Windows
::  Todo automatico: instala, configura hosts, SSL, firewall,
::  auto-start y abre navegador. Sin intervencion del usuario.
:: ============================================================

set BINARY_NAME=tsc-bridge.exe
set INSTALL_DIR=%LOCALAPPDATA%\tsc-bridge
set CONFIG_DIR=%APPDATA%\tsc-bridge
set CERT_DIR=%CONFIG_DIR%\certs
set STARTUP_DIR=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup
set HOSTNAME=myprinter.com
set HOSTS_FILE=%SystemRoot%\System32\drivers\etc\hosts
set SCRIPT_DIR=%~dp0

:: --- Auto-elevate to admin (silently) ---
net session >nul 2>&1
if %errorlevel% neq 0 (
    powershell -WindowStyle Hidden -Command "Start-Process cmd -ArgumentList '/c \"%~f0\"' -Verb RunAs -Wait" >nul 2>&1
    exit /b
)

:: --- Kill existing instance ---
taskkill /IM %BINARY_NAME% /F >nul 2>&1
timeout /t 1 /nobreak >nul

:: --- Install binary ---
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"
if not exist "%CONFIG_DIR%" mkdir "%CONFIG_DIR%"

if exist "%SCRIPT_DIR%%BINARY_NAME%" (
    copy /Y "%SCRIPT_DIR%%BINARY_NAME%" "%INSTALL_DIR%\%BINARY_NAME%" >nul
) else (
    exit /b 1
)

:: --- Add hostname to hosts file ---
findstr /C:"%HOSTNAME%" "%HOSTS_FILE%" >nul 2>&1
if %errorlevel% neq 0 (
    echo 127.0.0.1  %HOSTNAME%>> "%HOSTS_FILE%"
)

:: --- Clean old firewall rules (port 9271/9272 from v2.2.0 and earlier) ---
netsh advfirewall firewall delete rule name="TSC Bridge HTTP" >nul 2>&1
netsh advfirewall firewall delete rule name="TSC Bridge HTTPS" >nul 2>&1

:: --- Add firewall rules (ports 9638 and 9639) ---
netsh advfirewall firewall add rule name="TSC Bridge HTTP" dir=in action=allow protocol=TCP localport=9638 >nul 2>&1
netsh advfirewall firewall add rule name="TSC Bridge HTTPS" dir=in action=allow protocol=TCP localport=9639 >nul 2>&1

:: --- Generate certs: run bridge briefly then kill ---
if not exist "%CERT_DIR%" mkdir "%CERT_DIR%"
del /Q "%CONFIG_DIR%\.browser-opened" >nul 2>&1

start /B "" "%INSTALL_DIR%\%BINARY_NAME%"
timeout /t 4 /nobreak >nul
taskkill /IM %BINARY_NAME% /F >nul 2>&1
timeout /t 1 /nobreak >nul

:: --- Trust CA certificate in Windows cert store ---
set CA_CERT=%CERT_DIR%\ca.pem
if exist "%CA_CERT%" (
    certutil -addstore -f "Root" "%CA_CERT%" >nul 2>&1
)

:: --- Remove old startup shortcut before recreating ---
if exist "%STARTUP_DIR%\TSC Bridge.lnk" del "%STARTUP_DIR%\TSC Bridge.lnk" >nul 2>&1

:: --- Create startup shortcut (headless service mode at login, no browser) ---
>"%TEMP%\_tsc_startup.vbs" (
    echo Set oWS = WScript.CreateObject^("WScript.Shell"^)
    echo sLinkFile = "%STARTUP_DIR%\TSC Bridge.lnk"
    echo Set oLink = oWS.CreateShortcut^(sLinkFile^)
    echo oLink.TargetPath = "%INSTALL_DIR%\%BINARY_NAME%"
    echo oLink.Arguments = "--headless"
    echo oLink.WorkingDirectory = "%INSTALL_DIR%"
    echo oLink.Description = "TSC Bridge - Servicio de impresion"
    echo oLink.WindowStyle = 7
    echo oLink.Save
)
cscript //nologo "%TEMP%\_tsc_startup.vbs" >nul 2>&1
del "%TEMP%\_tsc_startup.vbs" >nul 2>&1

:: --- Create desktop shortcut (opens embedded dashboard) ---
set DESKTOP_DIR=%USERPROFILE%\Desktop
>"%TEMP%\_tsc_desktop.vbs" (
    echo Set oWS = WScript.CreateObject^("WScript.Shell"^)
    echo sLinkFile = "%DESKTOP_DIR%\TSC Bridge.lnk"
    echo Set oLink = oWS.CreateShortcut^(sLinkFile^)
    echo oLink.TargetPath = "%INSTALL_DIR%\%BINARY_NAME%"
    echo oLink.WorkingDirectory = "%INSTALL_DIR%"
    echo oLink.Description = "TSC Bridge - Panel de control"
    echo oLink.WindowStyle = 1
    echo oLink.Save
)
cscript //nologo "%TEMP%\_tsc_desktop.vbs" >nul 2>&1
del "%TEMP%\_tsc_desktop.vbs" >nul 2>&1

:: --- Start the bridge service (headless, stays running in background) ---
start "" "%INSTALL_DIR%\%BINARY_NAME%" --headless

:: Done — no pause, fully unattended
exit /b 0
