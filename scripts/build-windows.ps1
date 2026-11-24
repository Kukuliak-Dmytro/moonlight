$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$Dist = Join-Path $Root "dist"
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

Push-Location $Root
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"

    Write-Host "Building Windows (amd64)..."
    go build -trimpath -ldflags="-s -w" -o (Join-Path $Dist "moonlight-windows-amd64.exe") ./cmd/moonlight

    Write-Host "`nDone! Built:"
    Get-Item (Join-Path $Dist "moonlight-windows-amd64.exe") | Format-Table Name, Length
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
