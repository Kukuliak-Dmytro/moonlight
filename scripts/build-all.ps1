$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$Dist = Join-Path $Root "dist"
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

Push-Location $Root
try {
    $env:CGO_ENABLED = "0"

    Write-Host "Building Windows (amd64)..."
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    go build -trimpath -ldflags="-s -w" -o (Join-Path $Dist "monlight-windows-amd64.exe") ./cmd/monlight

    Write-Host "Building macOS Apple Silicon (arm64)..."
    $env:GOOS = "darwin"; $env:GOARCH = "arm64"
    go build -trimpath -ldflags="-s -w" -o (Join-Path $Dist "monlight-darwin-arm64") ./cmd/monlight

    Write-Host "Building macOS Intel (amd64)..."
    $env:GOOS = "darwin"; $env:GOARCH = "amd64"
    go build -trimpath -ldflags="-s -w" -o (Join-Path $Dist "monlight-darwin-amd64") ./cmd/monlight

    Write-Host "`nDone! Artifacts in $Dist:"
    Get-ChildItem $Dist | Format-Table Name, Length
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
