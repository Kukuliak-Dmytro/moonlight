param(
    [string]$BinaryPath = (Join-Path (Split-Path -Parent $PSScriptRoot) "dist\monlight-windows-amd64.exe"),
    [string]$TaskName = "MonlightDaemon"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $BinaryPath)) {
    throw "Binary not found at $BinaryPath. Build it first with .\scripts\build-all.ps1"
}

$FullBinary = (Resolve-Path $BinaryPath).Path
$Action = New-ScheduledTaskAction -Execute $FullBinary -Argument "daemon"
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
    -Description "Automatic DDC/CI monitor brightness & night light schedule (monlight)" `
    -Force | Out-Null

Start-ScheduledTask -TaskName $TaskName
Write-Host "Installed and started Scheduled Task '$TaskName' using $FullBinary"
