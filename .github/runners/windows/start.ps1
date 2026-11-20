# Why: trying the self-hosted Windows runner by hand, as the logged-in user,
# should take one command. The nightly gate's runner is not this one: it is
# the Windows service service.ps1 installs, under an account of its own.
#
# What: downloads the GitHub Actions runner (checked against its SHA-256),
# registers it with the repository the first time, starts it, and with
# -Autostart adds a Startup-folder shortcut so it starts at logon. -Dir is
# where the runner lives (default actions-runner\mk-windows on the system
# drive); pass the same -Dir on every start. service.ps1, given the same
# -Dir, stops this runner, removes its shortcut and replaces its
# registration.
#
# How: the runner runs as the current user, with that user's files and gh
# login, and has no job-started hook, so any job that reaches it runs as the
# maintainer: stop it and remove its registration when done. It sees the Go
# and LLVM installs mk needs (Go 1.27, LLVM 21 in C:\Program Files\LLVM, Git
# for Windows' bash, Node.js for the VS Code extension). Registration takes
# a token from `gh api`, so the gh login must have admin rights on the
# repository.
param(
    [string]$Repo = "zerodayokay/Mk-Lang",
    [string]$Dir = (Join-Path $env:SystemDrive "actions-runner\mk-windows"),
    [string]$Name = "mk-windows",
    [switch]$Autostart
)
$ErrorActionPreference = "Stop"
$version = "2.338.2"
$sha256 = "a1f17aa987fc8158bc7521d284912267a0d01b7c2c4684b33c86783b5e8221cc"

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
if (-not (Test-Path (Join-Path $Dir "config.cmd"))) {
    $zip = Join-Path $env:TEMP "actions-runner-win-x64-$version.zip"
    Invoke-WebRequest -Uri "https://github.com/actions/runner/releases/download/v$version/actions-runner-win-x64-$version.zip" -OutFile $zip
    $got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $sha256) { throw "checksum mismatch for the runner: $got" }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $Dir)
    Remove-Item -Path $zip
}

if (-not (Test-Path (Join-Path $Dir ".runner"))) {
    $token = gh api -X POST "repos/$Repo/actions/runners/registration-token" --jq .token
    if (-not $token) { throw "no registration token: is gh logged in with admin rights on $Repo?" }
    & (Join-Path $Dir "config.cmd") --unattended --url "https://github.com/$Repo" --token $token --name $Name --labels mk --work _work --replace
    if ($LASTEXITCODE -ne 0) { throw "runner registration failed" }
}

if ($Autostart) {
    $startup = [Environment]::GetFolderPath("Startup")
    $link = (New-Object -ComObject WScript.Shell).CreateShortcut((Join-Path $startup "Mk GitHub runner.lnk"))
    # cmd's start /belownormal: the runner and every build and test it
    # starts run below normal priority, so the machine stays responsive.
    $link.TargetPath = Join-Path $env:SystemRoot "System32\cmd.exe"
    $link.Arguments = "/c start `"`" /belownormal /min run.cmd"
    $link.WorkingDirectory = $Dir
    $link.WindowStyle = 7
    $link.Save()
}

Start-Process -FilePath (Join-Path $env:SystemRoot "System32\cmd.exe") -ArgumentList '/c start "" /belownormal /min run.cmd' -WorkingDirectory $Dir -WindowStyle Hidden
Write-Host "Windows runner $Name started; check it under Settings > Actions > Runners."
