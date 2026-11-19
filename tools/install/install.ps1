# Why: a Windows user needs one command that installs a released Mk
# toolchain, checked against its published hash, without admin rights.
#
# What: installs the Mk toolchain on Windows. Windows PowerShell 5.1 or
# later; never asks for elevation.
#   irm <release address>/latest/download/install.ps1 | iex
#   powershell -File install.ps1 [-Version 0.1.0] [-Root <dir>] [-Releases <address>] [-NoModifyPath]
#
# How: it downloads the newest release (or -Version) with its SHA256SUMS
# over https, checks the archive's SHA-256, unpacks it to
# <Root>\toolchains\<version>, points the junction <Root>\current at it,
# and adds <Root>\current\bin to the user's PATH (HKCU\Environment) unless
# -NoModifyPath. Root defaults to %LOCALAPPDATA%\mk. A first install trusts
# https and the release host; every later `mk upgrade` checks the release's
# Ed25519 signature. The parameters also come from MK_VERSION, MK_HOME,
# MK_RELEASES and MK_NO_MODIFY_PATH=1, for `irm ... | iex`.
#
# The whole script is one script block run in its own scope, so `irm | iex`
# leaves no preference, strict mode, function or variable in the caller's
# session. -JoinPathOnly <value>,<entry> prints the user PATH value the
# script would write and does nothing else (tests).

& {
[CmdletBinding()]
param(
    [string]$Version = $env:MK_VERSION,
    [string]$Root = $env:MK_HOME,
    [string]$Releases = $env:MK_RELEASES,
    [switch]$NoModifyPath,
    [string[]]$JoinPathOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2

$MkDefaultReleases = 'https://github.com/zerodayokay/Mk-Lang/releases'
$MkHost = 'x86_64-windows'
$MkVersionToken = '^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$'

# Join-MkPath is the user PATH value with entry added at the front, unless
# it is already there (compared without case or a trailing backslash).
function Join-MkPath([string]$PathValue, [string]$Entry) {
    $norm = { param($p) $p.Trim().TrimEnd('\').ToLowerInvariant() }
    $parts = @()
    if ($PathValue) { $parts = @($PathValue.Split(';') | Where-Object { $_ -ne '' }) }
    foreach ($p in $parts) {
        if ((& $norm $p) -eq (& $norm $Entry)) { return $PathValue }
    }
    return (@($Entry) + $parts) -join ';'
}

# Test-MkAddress accepts https, and http only to a loopback host (tests).
function Test-MkAddress([string]$Address) {
    if ($Address -match '^https://') { return $true }
    return $Address -match '^http://(127\.0\.0\.1|localhost|\[::1\])(:\d+)?(/|$)'
}

function Get-MkFileUrl([string]$Base, [string]$Ver, [string]$File) {
    $b = $Base.TrimEnd('/')
    if ($Ver) { return "$b/download/v$Ver/$File" }
    return "$b/latest/download/$File"
}

# Save-MkFile downloads Url to OutFile, following at most ten redirects
# itself so that each target is checked: https, or http only on a loopback
# host, so a redirect cannot downgrade the download. HttpWebRequest works the same in Windows
# PowerShell 5.1 and PowerShell 7.
function Save-MkFile([string]$Url, [string]$OutFile) {
    for ($i = 0; $i -le 10; $i++) {
        if (-not (Test-MkAddress $Url)) { throw "redirected to $Url, which is not https; nothing was installed" }
        $req = [Net.HttpWebRequest]::Create($Url)
        $req.AllowAutoRedirect = $false
        $req.UserAgent = 'mk-install'
        try {
            $resp = $req.GetResponse()
        } catch [Net.WebException] {
            if ($null -eq $_.Exception.Response) { throw }
            $resp = $_.Exception.Response
        }
        try {
            $code = [int]$resp.StatusCode
            if ($code -ge 300 -and $code -lt 400) {
                $loc = $resp.Headers['Location']
                if (-not $loc) { throw "$Url answered $code with no Location" }
                $Url = ([Uri]::new([Uri]$Url, $loc)).AbsoluteUri
                continue
            }
            if ($code -ne 200) { throw "$Url answered $code" }
            $in = $resp.GetResponseStream()
            $out = [IO.File]::Create($OutFile)
            try { $in.CopyTo($out) } finally { $out.Dispose(); $in.Dispose() }
            return
        } finally {
            $resp.Dispose()
        }
    }
    throw "more than 10 redirects from $Url"
}

# Compare-MkVersion is -1, 0 or 1 for major.minor.patch[-pre].
function Compare-MkVersion([string]$A, [string]$B) {
    $pa = $A.Split('-', 2); $pb = $B.Split('-', 2)
    $na = @($pa[0].Split('.') | ForEach-Object { [int]$_ })
    $nb = @($pb[0].Split('.') | ForEach-Object { [int]$_ })
    for ($i = 0; $i -lt 3; $i++) {
        if ($na[$i] -lt $nb[$i]) { return -1 }
        if ($na[$i] -gt $nb[$i]) { return 1 }
    }
    $ra = if ($pa.Count -gt 1) { $pa[1] } else { '' }
    $rb = if ($pb.Count -gt 1) { $pb[1] } else { '' }
    if ($ra -eq $rb) { return 0 }
    if ($ra -eq '') { return 1 }
    if ($rb -eq '') { return -1 }
    return [string]::CompareOrdinal($ra, $rb)
}

# Get-MkSha256 is the file's SHA-256 in lower-case hex, from .NET rather
# than Get-FileHash: in Windows PowerShell 5.1 Get-FileHash is a function of
# Microsoft.PowerShell.Utility's script module, which does not load when
# PSModulePath is PowerShell 7's (powershell.exe started from pwsh).
function Get-MkSha256([string]$File) {
    $sha = [Security.Cryptography.SHA256]::Create()
    $f = [IO.File]::OpenRead($File)
    try {
        $h = $sha.ComputeHash($f)
    } finally {
        $f.Dispose()
        $sha.Dispose()
    }
    return ([BitConverter]::ToString($h) -replace '-', '').ToLowerInvariant()
}

# Select-MkArchive reads SHA256SUMS and returns the host's archive (the
# version asked for, or the newest listed) as name, version and hash. The
# version is a version token only (no '/' or '..').
function Select-MkArchive([string]$Sums, [string]$Ver) {
    $best = $null
    foreach ($line in $Sums.Split("`n")) {
        $line = $line.TrimEnd("`r")
        if ($line -cnotmatch '^([0-9a-f]{64})  (mk-(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)-x86_64-windows\.zip)$') { continue }
        $c = @{ Hash = $Matches[1]; Name = $Matches[2]; Version = $Matches[3] }
        if ($Ver) {
            if ($c.Version -ceq $Ver) { return $c }
            continue
        }
        if ($null -eq $best -or (Compare-MkVersion $c.Version $best.Version) -gt 0) { $best = $c }
    }
    if ($null -eq $best) {
        $what = if ($Ver) { "mk $Ver" } else { 'the release' }
        throw "$what has no $MkHost archive in its SHA256SUMS"
    }
    return $best
}

# Set-MkJunction points the junction Link at Target: a new junction beside
# it, the old one moved aside, the new one renamed into place, the old one
# removed (the junction only, never what it points at).
function Set-MkJunction([string]$Link, [string]$Target) {
    $dir = Split-Path -Parent $Link
    $new = Join-Path $dir ('.current-new-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Junction -Path $new -Target $Target | Out-Null
    $old = $null
    if (Test-Path -LiteralPath $Link) {
        $old = Join-Path $dir ('.current-old-' + [guid]::NewGuid().ToString('N'))
        [IO.Directory]::Move($Link, $old)
    }
    try {
        [IO.Directory]::Move($new, $Link)
    } catch {
        if ($old) { [IO.Directory]::Move($old, $Link) }
        [IO.Directory]::Delete($new, $false)
        throw
    }
    # Directory.Delete with recursive false removes a junction itself.
    if ($old) { [IO.Directory]::Delete($old, $false) }
}

# Add-MkUserPath puts Entry at the front of the user PATH. It reads the
# value unexpanded and writes it back as REG_EXPAND_SZ, so entries such as
# %USERPROFILE%\bin stay as written, then announces
# the change to running programs: setting and clearing a user variable
# through [Environment] broadcasts WM_SETTINGCHANGE. Untested by design:
# no test touches the real registry.
function Add-MkUserPath([string]$Entry) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $old = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $new = Join-MkPath $old $Entry
        if ($new -eq $old) { return $false }
        $key.SetValue('Path', $new, [Microsoft.Win32.RegistryValueKind]::ExpandString)
    } finally {
        $key.Dispose()
    }
    [Environment]::SetEnvironmentVariable('MK_INSTALL_PATH_CHANGED', '1', 'User')
    [Environment]::SetEnvironmentVariable('MK_INSTALL_PATH_CHANGED', $null, 'User')
    return $true
}

function Install-Mk {
    if (-not $Releases) { $Releases = $MkDefaultReleases }
    if (-not $Root) { $Root = Join-Path $env:LOCALAPPDATA 'mk' }
    # A relative root is the working directory's, made absolute now.
    $Root = [IO.Path]::GetFullPath([IO.Path]::Combine((Get-Location -PSProvider FileSystem).ProviderPath, $Root))
    if ($env:MK_NO_MODIFY_PATH -eq '1') { $NoModifyPath = $true }
    if (-not (Test-MkAddress $Releases)) { throw "the release address $Releases is not https" }
    if ($Version) {
        $Version = $Version -replace '^v', ''
        if ($Version -cnotmatch $MkVersionToken) { throw "-Version $Version is not a version (major.minor.patch[-pre])" }
    }
    $ProgressPreference = 'SilentlyContinue'
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

    $toolchains = Join-Path $Root 'toolchains'
    New-Item -ItemType Directory -Force -Path $toolchains | Out-Null
    $work = Join-Path $toolchains ('.install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $work | Out-Null
    try {
        $sumsFile = Join-Path $work 'SHA256SUMS'
        Save-MkFile (Get-MkFileUrl $Releases $Version 'SHA256SUMS') $sumsFile
        $a = Select-MkArchive ([IO.File]::ReadAllText($sumsFile)) $Version
        $dest = Join-Path $toolchains $a.Version
        if (Test-Path -LiteralPath $dest) {
            Write-Output "mk $($a.Version) is already in $dest"
        } else {
            $zip = Join-Path $work $a.Name
            Save-MkFile (Get-MkFileUrl $Releases $a.Version $a.Name) $zip
            $got = Get-MkSha256 $zip
            if ($got -ne $a.Hash) { throw "$($a.Name): SHA-256 $got, but SHA256SUMS says $($a.Hash); nothing was installed" }
            $tree = Join-Path $work 'tree'
            Add-Type -AssemblyName System.IO.Compression.FileSystem
            [IO.Compression.ZipFile]::ExtractToDirectory($zip, $tree)
            $top = Join-Path $tree "mk-$($a.Version)-$MkHost"
            if (-not (Test-Path -LiteralPath (Join-Path $top 'bin\mk.exe'))) { throw "$($a.Name) holds no bin\mk.exe; nothing was installed" }
            [IO.Directory]::Move($top, $dest)
            Write-Output "unpacked mk $($a.Version) to $dest"
        }
        Set-MkJunction (Join-Path $Root 'current') $dest
        Write-Output "current is mk $($a.Version)"
    } finally {
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }

    $entry = Join-Path $Root 'current\bin'
    if ($NoModifyPath) {
        Write-Output "PATH was not changed; add this directory to your PATH: $entry"
    } elseif (Add-MkUserPath $entry) {
        Write-Output "added $entry to your user PATH; open a new terminal to use mk"
    } else {
        Write-Output "$entry is already on your user PATH"
    }
}

if ($JoinPathOnly) {
    $v = if ($JoinPathOnly.Count -gt 1) { $JoinPathOnly[0] } else { '' }
    Join-MkPath $v $JoinPathOnly[-1]
    return
}
Install-Mk
} @args
