# GENERATED — do not edit; edit cmd/gen-install/templates/install.ps1.tmpl + run `go run ./cmd/gen-install`
#
# One-line install (PowerShell):
#   iwr -useb https://raw.githubusercontent.com/samibel/graphi/main/install.ps1 | iex
#
# Downloads the matching graphi release asset, verifies it against the published
# SHA256SUMS (fail-closed), and installs it to $env:USERPROFILE\.local\bin.
# Idempotent (re-run overwrites cleanly).

[CmdletBinding()]
param(
    [string]$Version = $(if ($env:GRAPHI_VERSION) { $env:GRAPHI_VERSION } else { "latest" }),
    [string]$BinDir = $(if ($env:GRAPHI_BINDIR) { $env:GRAPHI_BINDIR } else { Join-Path $env:USERPROFILE ".local\bin" })
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ReleasesUrl = "https://github.com/samibel/graphi/releases"

# Valid asset names — GENERATED from internal/release.ReleaseTargets via
# release.AssetName. Single source of truth for accepted targets.
$ValidAssets = @(
    "graphi-linux-amd64"
    "graphi-linux-arm64"
    "graphi-darwin-amd64"
    "graphi-darwin-arm64"
    "graphi-windows-amd64.exe"
)

# graphi ships a single windows/amd64 asset.
$asset = "graphi-windows-amd64.exe"
if ($ValidAssets -notcontains $asset) {
    Write-Error "install.ps1: no windows release asset ($asset) in the supported set: $($ValidAssets -join ', ')"
    exit 1
}

if ($Version -eq "latest") {
    $base = "$ReleasesUrl/latest/download"
} else {
    $base = "$ReleasesUrl/download/$Version"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("graphi-install-" + [System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    $assetPath = Join-Path $tmp $asset
    $sumsPath = Join-Path $tmp "SHA256SUMS"

    Write-Host "install.ps1: downloading $asset ($Version)..."
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $assetPath
    Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sumsPath

    # Verify checksum (fail-closed).
    $expectedLine = Select-String -Path $sumsPath -Pattern ([Regex]::Escape($asset) + '$') | Select-Object -First 1
    if (-not $expectedLine) {
        Write-Error "install.ps1: $asset not found in SHA256SUMS; refusing to install"
        exit 1
    }
    $expected = ($expectedLine.Line -split '\s+')[0].ToLower()
    $actual = (Get-FileHash -Algorithm SHA256 -Path $assetPath).Hash.ToLower()
    if ($expected -ne $actual) {
        Write-Error "install.ps1: checksum verification FAILED for $asset (expected $expected, got $actual); refusing to install"
        exit 1
    }

    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    $dest = Join-Path $BinDir "graphi.exe"
    Copy-Item -Path $assetPath -Destination $dest -Force
    Write-Host "install.ps1: installed graphi to $dest"

    $pathParts = $env:PATH -split ';'
    if ($pathParts -notcontains $BinDir) {
        Write-Host "install.ps1: note: $BinDir is not on your PATH."
        Write-Host "  add it, e.g.:  setx PATH `"$BinDir;`$env:PATH`""
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
