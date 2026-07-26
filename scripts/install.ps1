# OpenSave installer for Windows.
#
#   irm https://raw.githubusercontent.com/Liquid-co/OpenSave/main/scripts/install.ps1 | iex
#
# Installs the CLI to %LOCALAPPDATA%\OpenSave\bin and puts it on your PATH, so
# `opensave` works from any terminal. Installs as `opensave.exe` — the name the
# documentation and the tool's own help use.
#
# Override with:
#   $env:OPENSAVE_INSTALL_DIR = 'C:\tools'   where to put it
#   $env:OPENSAVE_VERSION     = 'v2.2.0'     pin a version instead of latest
#
# Downloads are checksum-verified against the SHA256SUMS published with the
# release: piping a script to a shell is trust enough on its own.

$ErrorActionPreference = 'Stop'

$Repo      = 'Liquid-co/OpenSave'
$InstallDir = if ($env:OPENSAVE_INSTALL_DIR) { $env:OPENSAVE_INSTALL_DIR }
              else { Join-Path $env:LOCALAPPDATA 'OpenSave\bin' }
$Version   = if ($env:OPENSAVE_VERSION) { $env:OPENSAVE_VERSION } else { 'latest' }

function Fail($msg) { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

# ── Platform ─────────────────────────────────────────────────────────────

if ([Environment]::Is64BitOperatingSystem -ne $true) {
    Fail 'OpenSave requires 64-bit Windows.'
}

$base = if ($Version -eq 'latest') {
    "https://github.com/$Repo/releases/latest/download"
} else {
    "https://github.com/$Repo/releases/download/$Version"
}

# ── Download ─────────────────────────────────────────────────────────────

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("opensave-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    $asset   = 'opensave-cli.exe'
    $target  = Join-Path $tmp $asset

    Write-Host "==> Downloading OpenSave CLI ($Version)..."
    try {
        Invoke-WebRequest -Uri "$base/$asset" -OutFile $target -UseBasicParsing
    } catch {
        Fail "could not download $base/$asset"
    }

    # Verify when the release publishes checksums (releases before 2.2.0 do not).
    $sums = Join-Path $tmp 'SHA256SUMS'
    $haveSums = $true
    try {
        Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
    } catch { $haveSums = $false }

    if ($haveSums) {
        $expected = $null
        foreach ($line in Get-Content $sums) {
            # Tolerate both "<hash>  file" and "<hash> *file".
            $parts = $line -split '\s+'
            if ($parts.Count -ge 2 -and ($parts[-1].TrimStart('*') -eq $asset)) {
                $expected = $parts[0]; break
            }
        }
        if (-not $expected) { Fail "no checksum for $asset in SHA256SUMS" }
        $actual = (Get-FileHash -Path $target -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $expected.ToLower()) {
            Fail "checksum mismatch - refusing to install`n  expected: $expected`n  actual:   $actual"
        }
        Write-Host '==> Checksum verified.'
    } else {
        Write-Host 'warning: this release publishes no SHA256SUMS - skipping verification' -ForegroundColor Yellow
    }

    # ── Install ──────────────────────────────────────────────────────────

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $dest = Join-Path $InstallDir 'opensave.exe'

    # A running copy can't be overwritten; move it aside and let the next run
    # clean it up, which is how self-updating binaries handle this on Windows.
    if (Test-Path $dest) {
        $old = "$dest.old"
        Remove-Item $old -Force -ErrorAction SilentlyContinue
        try { Move-Item $dest $old -Force } catch {
            Fail "opensave.exe is running - close it and try again"
        }
    }
    Copy-Item $target $dest -Force
    Write-Host "==> Installed to $dest"

    # ── PATH ─────────────────────────────────────────────────────────────

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        $newPath = if ([string]::IsNullOrEmpty($userPath)) { $InstallDir }
                   else { "$userPath;$InstallDir" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        # Also update this session, so it works without reopening the terminal.
        $env:Path = "$env:Path;$InstallDir"
        Write-Host "==> Added $InstallDir to your user PATH."
        Write-Host '    Open a new terminal for it to apply everywhere.' -ForegroundColor Yellow
    }

    $installed = & $dest version 2>$null
    Write-Host ''
    Write-Host "==> $installed"
    Write-Host ''
    Write-Host 'Next steps:'
    Write-Host '  opensave                 status panel'
    Write-Host '  opensave scan            find your game saves'
    Write-Host '  opensave daemon start    run the sync service'
    Write-Host ''
    Write-Host "The desktop app is a separate download: https://github.com/$Repo/releases/latest"
}
finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
