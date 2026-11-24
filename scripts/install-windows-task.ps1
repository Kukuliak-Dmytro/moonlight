param(
    [string]$BinaryPath = (Join-Path (Split-Path -Parent $PSScriptRoot) "dist\moonlight-windows-amd64.exe"),
    [string]$TaskName = "MoonlightDaemon"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
    throw "Binary not found at $BinaryPath. Build it first with .\scripts\build-all.ps1"
}

$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\moonlight"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$InstalledExe = Join-Path $InstallDir "moonlight.exe"

Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Stop-Process -Name "moonlight" -Force -ErrorAction SilentlyContinue
Stop-Process -Name "moonlight-windows-amd64" -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 300

Copy-Item -Force (Resolve-Path $BinaryPath).Path $InstalledExe

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not ($UserPath -split ";" | Where-Object { $_ -ieq $InstallDir })) {
    $NewPath = if ([string]::IsNullOrWhiteSpace($UserPath)) { $InstallDir } else { "$UserPath;$InstallDir" }
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    Write-Host "Added $InstallDir to your User PATH (reopen terminal to use 'moonlight' anywhere)"
}

$FullBinary = $InstalledExe
$HiddenRunner = (Resolve-Path (Join-Path $PSScriptRoot "run-daemon-hidden.vbs")).Path
$Action = New-ScheduledTaskAction -Execute "wscript.exe" -Argument "`"$HiddenRunner`" `"$FullBinary`""
$Trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$Settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $Action `
    -Trigger $Trigger `
    -Settings $Settings `
    -Description "Automatic DDC/CI monitor brightness & night light schedule (moonlight)" `
    -Force | Out-Null

Start-ScheduledTask -TaskName $TaskName
Write-Host "Installed and started hidden Scheduled Task '$TaskName' using $FullBinary"
