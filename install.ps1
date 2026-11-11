<#
.SYNOPSIS
  Builds and installs qemu-tui on Windows.

.DESCRIPTION
  Builds qemu-tui from this source tree with Go, copies qemu-tui.exe (and the docs)
  to the install folder, and adds that folder to your user PATH.
  Run it from the source directory. No administrator rights are needed with the default prefix.

  If scripts are blocked on your machine, run:
      powershell -ExecutionPolicy Bypass -File .\install.ps1

  Windows support is experimental: QEMU is controlled over a localhost TCP port.

.PARAMETER Prefix
  Install folder. Default: %LOCALAPPDATA%\Programs\qemu-tui

.PARAMETER Uninstall
  Remove the installed files and the PATH entry. Your VMs and settings are kept.

.PARAMETER NoPath
  Do not change the user PATH.

.PARAMETER SkipBuild
  Do not build; install the existing bin\qemu-tui.exe.

.PARAMETER InstallDeps
  Try to install Go and QEMU with winget when they are missing.

.PARAMETER Yes
  Do not ask for confirmation.

.PARAMETER DryRun
  Show what would be done without changing anything.

.EXAMPLE
  .\install.ps1
.EXAMPLE
  .\install.ps1 -InstallDeps -Yes
.EXAMPLE
  .\install.ps1 -Uninstall
#>
[CmdletBinding()]
param(
    [string]$Prefix = (Join-Path $env:LOCALAPPDATA 'Programs\qemu-tui'),
    [switch]$Uninstall,
    [switch]$NoPath,
    [switch]$SkipBuild,
    [switch]$InstallDeps,
    [switch]$Yes,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
$App = 'qemu-tui'
$MinGo = [version]'1.22'
$ExePath = Join-Path $Prefix "$App.exe"
$DocDir = Join-Path $Prefix 'doc'

function Info([string]$Msg) { Write-Host "==> $Msg" -ForegroundColor Cyan }
function Ok([string]$Msg)   { Write-Host " ok  $Msg" -ForegroundColor Green }
function Warn([string]$Msg) { Write-Host "warning: $Msg" -ForegroundColor Yellow }
function Die([string]$Msg)  { Write-Host "error: $Msg" -ForegroundColor Red; exit 1 }

# Runs a script block, or only prints the description in dry-run mode.
function Run([scriptblock]$Block, [string]$Desc) {
    if ($DryRun) { Write-Host "  (dry-run) $Desc" } else { & $Block }
}

# Runs a native command and stops when it fails.
function Invoke-Native([string]$Exe, [string[]]$ArgList) {
    & $Exe @ArgList
    if ($LASTEXITCODE -ne 0) { Die "'$Exe $($ArgList -join ' ')' failed (exit code $LASTEXITCODE)" }
}

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = (@($machine, $user) | Where-Object { $_ }) -join ';'
}

function Test-InPath([string]$Dir, [string]$PathValue) {
    if (-not $PathValue) { return $false }
    $want = $Dir.TrimEnd('\')
    foreach ($p in $PathValue.Split(';')) {
        if ($p.TrimEnd('\') -ieq $want) { return $true }
    }
    return $false
}

function Add-ToUserPath([string]$Dir) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (Test-InPath $Dir $current) { Ok "$Dir is already in your PATH"; return }
    Run {
        $new = if ($current) { $current.TrimEnd(';') + ';' + $Dir } else { $Dir }
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        $env:Path = $env:Path.TrimEnd(';') + ';' + $Dir
    } "add $Dir to the user PATH"
    Ok "added $Dir to your user PATH (open a new terminal to use it)"
}

function Remove-FromUserPath([string]$Dir) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (Test-InPath $Dir $current)) { return }
    Run {
        $keep = $current.Split(';') | Where-Object { $_ -and ($_.TrimEnd('\') -ine $Dir.TrimEnd('\')) }
        [Environment]::SetEnvironmentVariable('Path', ($keep -join ';'), 'User')
    } "remove $Dir from the user PATH"
    Ok "removed $Dir from your user PATH"
}

function Get-GoVersion {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { return $null }
    $out = (& go version) 2>$null
    if ($out -match 'go(\d+)\.(\d+)') {
        return [version]('{0}.{1}' -f $Matches[1], $Matches[2])
    }
    return $null
}

function Install-WithWinget([string]$Id) {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        Warn "winget was not found; install $Id manually"
        return
    }
    Info "Installing $Id with winget"
    Run {
        & winget install --id $Id -e --accept-package-agreements --accept-source-agreements
        if ($LASTEXITCODE -ne 0) { Warn "winget could not install $Id" }
    } "winget install $Id"
}

# ---------------------------------------------------------------- uninstall

if ($Uninstall) {
    Info "Removing $App from $Prefix"
    Run { Remove-Item -Force -ErrorAction SilentlyContinue $ExePath } "delete $ExePath"
    Run { Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $DocDir } "delete $DocDir"
    Run {
        if ((Test-Path $Prefix) -and -not (Get-ChildItem -Force $Prefix)) { Remove-Item -Force $Prefix }
    } "delete $Prefix if empty"
    if (-not $NoPath) { Remove-FromUserPath $Prefix }
    if (-not $DryRun) { Ok "removed $App" }
    Write-Host "Your VMs and settings are untouched: $HOME\.local\share\qemu-tui and $HOME\.config\qemu-tui"
    exit 0
}

# ---------------------------------------------------------------- checks

$src = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $src
if (-not ((Test-Path 'go.mod') -and (Test-Path "cmd\$App"))) {
    Die "run this script from the $App source directory (go.mod not found)"
}

if ($InstallDeps) {
    if (-not $SkipBuild -and -not (Get-GoVersion)) { Install-WithWinget 'GoLang.Go' }
    if (-not (Get-Command 'qemu-system-x86_64.exe' -ErrorAction SilentlyContinue)) {
        Install-WithWinget 'SoftwareFreedomConservancy.QEMU'
    }
    Refresh-Path
}

# ---------------------------------------------------------------- build

if (-not $SkipBuild) {
    $ver = Get-GoVersion
    if (-not $ver) {
        Die 'Go is required to build. Install it from https://go.dev/dl/ or run: winget install GoLang.Go  (or re-run with -InstallDeps)'
    }
    if ($ver -lt $MinGo) { Die "Go $MinGo or newer is required (found $ver)" }
    Ok "Go $ver"
    Info "Building $App"
    if (-not (Test-Path 'go.sum')) {
        Run { Invoke-Native 'go' @('mod', 'tidy') } 'go mod tidy'
    }
    Run {
        New-Item -ItemType Directory -Force -Path 'bin' | Out-Null
        Invoke-Native 'go' @('build', '-ldflags', '-s -w', '-o', "bin\$App.exe", ".\cmd\$App")
    } "go build -o bin\$App.exe"
} else {
    if (-not $DryRun -and -not (Test-Path "bin\$App.exe")) { Die "-SkipBuild needs an existing bin\$App.exe" }
    Info "Skipping build (using bin\$App.exe)"
}

# ---------------------------------------------------------------- confirm

Write-Host ''
Info 'Installation plan'
Write-Host "  program : $ExePath"
if (Test-Path 'man\qemu-tui.1') { Write-Host "  docs    : $DocDir (README and man page source)" }
if (-not $NoPath) { Write-Host "  PATH    : $Prefix will be added to your user PATH" }
if (-not $Yes -and -not $DryRun) {
    try {
        $answer = Read-Host 'Continue? [Y/n]'
        if ($answer -match '^(n|no)$') { Die 'cancelled' }
    } catch {
        # not interactive: continue
    }
}

# ---------------------------------------------------------------- install

Run { New-Item -ItemType Directory -Force -Path $Prefix | Out-Null } "create $Prefix"
Run { Copy-Item -Force "bin\$App.exe" $ExePath } "copy bin\$App.exe to $ExePath"
if (-not $DryRun) { Ok "installed $ExePath" }

$docs = @('README.md', 'man\qemu-tui.1') | Where-Object { Test-Path $_ }
if ($docs) {
    Run {
        New-Item -ItemType Directory -Force -Path $DocDir | Out-Null
        foreach ($d in $docs) { Copy-Item -Force $d $DocDir }
    } "copy docs to $DocDir"
}

if (-not $NoPath) { Add-ToUserPath $Prefix }

# ---------------------------------------------------------------- advice

$missing = @()
foreach ($tool in @('qemu-system-x86_64.exe', 'qemu-img.exe')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { $missing += $tool }
}
if ($missing.Count -gt 0) {
    Warn ("not found in PATH: " + ($missing -join ', '))
    $pf = Join-Path $env:ProgramFiles 'qemu'
    if (Test-Path (Join-Path $pf 'qemu-system-x86_64.exe')) {
        Write-Host "         QEMU is installed in $pf but that folder is not in your PATH: add it (Settings > System > About > Advanced system settings > Environment Variables)."
    } else {
        Write-Host '         install QEMU with: winget install SoftwareFreedomConservancy.QEMU'
        Write-Host '         or download it from https://www.qemu.org/download/#windows'
    }
}
Write-Host 'note: for hardware acceleration enable the "Windows Hypervisor Platform" optional feature; otherwise QEMU uses slower software emulation.'
Write-Host 'note: Windows support is experimental.'

Write-Host ''
if ($DryRun) {
    Ok 'dry run finished; nothing was changed'
} else {
    Ok "done. Open a new terminal and run: $App    Uninstall: .\install.ps1 -Uninstall"
}
