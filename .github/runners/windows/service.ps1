# Why: the self-hosted Windows runner runs only the nightly gate, and it
# should not run as the maintainer: a job that runs as that user can read
# the user's files and use the user's gh login, which has admin rights on the
# repository.
#
# What: installs the runner in -Dir as a Windows service under its own
# virtual account, NT SERVICE\<service name>: no password, no gh login, not
# an administrator, and only the privileges SeChangeNotifyPrivilege,
# SeCreateGlobalPrivilege and SeIncreaseWorkingSetPrivilege (no
# SeImpersonatePrivilege). The service has a restricted service SID, so its
# processes can write only where the account itself (or Everyone) is
# granted write access, not where a group such as Authenticated Users is. A
# job-started hook fails every job but the nightly workflow of -Repo on main,
# started by its schedule or by hand. The account can read and run -Dir but
# write only its work, diagnostics, cache, temp and home directories (-Work,
# -Cache, -Temp, inside -Dir by default), and read the evidence directory
# (-Evidence). On every fixed drive but the system drive it is denied
# reading, writing and deleting from the root down, except where it is
# granted access below. The runner and every build and test it starts run
# at below-normal CPU priority. A runner that start.ps1 installed in -Dir is
# stopped, its Startup shortcut removed and its registration replaced; pass
# the same -Dir to keep the install where it is.
#
# How: run it from an elevated PowerShell in which gh is logged in with
# admin rights on the repository, once and again for every new runner
# version (the runner does not update itself):
#
#   pwsh -NoProfile -File .github/runners/windows/service.ps1 -Evidence <evidence directory>
#
# Each run deletes the old service and registration, extracts the runner
# from its checked zip over -Dir again (so no file the account could have
# changed runs elevated), registers it with --disableupdate, and creates the
# service with sc.exe, stopped, so no job runs before the account, its
# privileges, its file access and the hook are in place. The registration
# token comes from gh in this shell and reaches config.cmd through an
# environment variable, never a command line or a file. Go, LLVM and Git for
# Windows must be installed for all users (Go in C:\Program Files\Go, LLVM
# in C:\Program Files\LLVM, Git in C:\Program Files\Git), since the service
# account cannot read the maintainer's profile. The service account has no
# WSL distribution of its own, so it cannot run the gate's
# Linux-through-WSL legs.
param(
    [string]$Repo = "zerodayokay/Mk-Lang",
    [string]$Dir = (Join-Path $env:SystemDrive "actions-runner\mk-windows"),
    [string]$Name = "mk-windows",
    [string]$Work = "",
    [string]$Cache = "",
    [string]$Temp = "",
    [string]$Evidence = ""
)
$ErrorActionPreference = "Stop"
$version = "2.338.2"
$sha256 = "a1f17aa987fc8158bc7521d284912267a0d01b7c2c4684b33c86783b5e8221cc"

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "run this from an elevated PowerShell"
}
# The repository and runner names go into the hook script and the service
# name: plain names only.
if ($Repo -cnotmatch '^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$') { throw "-Repo $Repo is not <owner>/<repository>" }
if ($Name -cnotmatch '^[A-Za-z0-9._-]+$') { throw "-Name $Name holds characters other than letters, digits, '.', '_' and '-'" }
$Dir = [IO.Path]::GetFullPath($Dir)
if (-not $Work) { $Work = Join-Path $Dir "_work" }
if (-not $Cache) { $Cache = Join-Path $Dir "cache" }
if (-not $Temp) { $Temp = Join-Path $Dir "tmp" }
$Work = [IO.Path]::GetFullPath($Work)
$Cache = [IO.Path]::GetFullPath($Cache)
$Temp = [IO.Path]::GetFullPath($Temp)
$diag = Join-Path $Dir "_diag"
$home_ = Join-Path $Dir "home"
$hooks = Join-Path $Dir "hooks"
if ($Evidence) {
    if (-not (Test-Path $Evidence)) { throw "no evidence directory at $Evidence" }
    $Evidence = [IO.Path]::GetFullPath($Evidence)
}
# The runner's own name for its service.
$service = "actions.runner.$($Repo.Replace('/', '-')).$Name"
$account = "NT SERVICE\$service"

# icacls runs a command and fails the script when it fails.
function Invoke-Icacls {
    & icacls.exe @args | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls $($args -join ' ') failed" }
}

function Invoke-Sc {
    $out = & sc.exe @args
    if ($LASTEXITCODE -ne 0) { throw "sc.exe $($args -join ' ') failed: $out" }
}

# icacls /T and Remove-Item -Recurse below act on trees the service account
# may have written. A symbolic link or junction there could point them at
# files elsewhere, so the script stops at one.
function Assert-NoLinks([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $items = @(Get-Item -LiteralPath $Path -Force) + @(Get-ChildItem -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue)
    $links = @($items | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint })
    if ($links) {
        $links | ForEach-Object { Write-Host "  link: $($_.FullName)" }
        throw "$Path holds links (above); remove them and run this again"
    }
}
foreach ($d in @($Dir, $Work, $Cache, $Temp, $Evidence)) { if ($d) { Assert-NoLinks $d } }

# 1. Nothing of the old install runs: the service (this one, or one an
# earlier config.cmd --runasservice made), a runner start.ps1 started, and
# its Startup shortcut. .service could have been written by the account, so
# only a runner service's name is taken from it.
$old = @($service)
$serviceFile = Join-Path $Dir ".service"
if (Test-Path $serviceFile) {
    $s = (Get-Content -Path $serviceFile -Raw).Trim()
    if ($s -cmatch '^actions\.runner\.[A-Za-z0-9._-]+$') { $old += $s } else { Write-Warning "$serviceFile does not name a runner service; ignored" }
}
foreach ($s in ($old | Select-Object -Unique)) {
    if (Get-Service -Name $s -ErrorAction SilentlyContinue) {
        Stop-Service -Name $s -Force
        Invoke-Sc delete $s
        # The name is free once the service manager has let go of it.
        for ($i = 0; $i -lt 60 -and (Get-Service -Name $s -ErrorAction SilentlyContinue); $i++) { Start-Sleep -Milliseconds 500 }
        if (Get-Service -Name $s -ErrorAction SilentlyContinue) { throw "the service $s is still marked for deletion; close services.msc and run this again" }
        Write-Host "deleted the service $s"
    }
}
foreach ($folder in @([Environment]::GetFolderPath("Startup"), [Environment]::GetFolderPath("CommonStartup"))) {
    $link = Join-Path $folder "Mk GitHub runner.lnk"
    if (Test-Path $link) { Remove-Item -Path $link }
}
Get-Process -Name Runner.Listener, Runner.Worker, RunnerService -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -and $_.Path.StartsWith($Dir, [StringComparison]::OrdinalIgnoreCase) } |
    Stop-Process -Force
# Again, now that no job of the old runner can add one.
foreach ($d in @($Dir, $Work, $Cache, $Temp, $Evidence)) { if ($d) { Assert-NoLinks $d } }

# 2. -Dir belongs to Administrators and SYSTEM. Every file below it loses
# its own entries (config.cmd's NETWORK SERVICE grant among them) and
# inherits from -Dir alone, which gives the account read and run access.
New-Item -ItemType Directory -Force -Path $Dir | Out-Null
Invoke-Icacls $Dir /reset /T /C /Q
Invoke-Icacls $Dir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" /Q
Write-Host "${Dir}: Administrators and SYSTEM full control, nothing inherited"

# 3. The old registration, removed through the API rather than by running
# the old config.cmd, and its local files.
$runnerFile = Join-Path $Dir ".runner"
if (Test-Path $runnerFile) {
    try { $id = "$((Get-Content -Path $runnerFile -Raw | ConvertFrom-Json).agentId)" } catch { $id = "" }
    if ($id -match '^[0-9]+$') {
        gh api -X DELETE "repos/$Repo/actions/runners/$id" | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Warning "could not delete runner $id from $Repo; --replace takes over its name" }
    }
}
# Every settings file the runner reads from its root, whether or not .runner
# exists: config.cmd refuses to register while .runner or .runner_migrated
# is there, and the listener reads .env before it configures.
foreach ($f in @(".runner", ".runner_migrated", ".credentials", ".credentials_migrated", ".credentials_rsaparams", ".service", ".env", ".path", ".options", ".certificates", ".setup_info")) {
    $p = Join-Path $Dir $f
    if (Test-Path -LiteralPath $p) { Remove-Item -LiteralPath $p -Recurse -Force }
}

# 4. The runner, from its zip checked against its SHA-256, over -Dir: bin
# and externals are replaced whole, every other file of the zip
# overwritten.
$zip = Join-Path $env:TEMP "actions-runner-win-x64-$version.zip"
Invoke-WebRequest -Uri "https://github.com/actions/runner/releases/download/v$version/actions-runner-win-x64-$version.zip" -OutFile $zip
$got = (Get-FileHash -Path $zip -Algorithm SHA256).Hash.ToLowerInvariant()
if ($got -ne $sha256) { throw "checksum mismatch for the runner: $got" }
foreach ($d in @("bin", "externals")) {
    $p = Join-Path $Dir $d
    if (Test-Path $p) { Remove-Item -Path $p -Recurse -Force }
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
[System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $Dir, $true)
Remove-Item -Path $zip

# 5. Registration, with the token in ACTIONS_RUNNER_INPUT_TOKEN, which
# config.cmd reads in place of --token.
$env:ACTIONS_RUNNER_INPUT_TOKEN = gh api -X POST "repos/$Repo/actions/runners/registration-token" --jq .token
if (-not $env:ACTIONS_RUNNER_INPUT_TOKEN) { throw "no registration token: is gh logged in with admin rights on $Repo?" }
try {
    & (Join-Path $Dir "config.cmd") --unattended --url "https://github.com/$Repo" --name $Name --labels mk --work $Work --replace --disableupdate
    $rc = $LASTEXITCODE
} finally {
    Remove-Item Env:ACTIONS_RUNNER_INPUT_TOKEN
}
if ($rc -ne 0) { throw "runner registration failed" }

# 6. The service, created stopped and on demand, given a restricted service
# SID, moved to its virtual account and its privileges cut, all before it
# ever starts.
$bin = Join-Path $Dir "bin\RunnerService.exe"
Invoke-Sc create $service "binPath=" "`"$bin`"" "start=" demand "DisplayName=" "GitHub Actions Runner ($service)"
Invoke-Sc sidtype $service restricted
Invoke-Sc config $service "obj=" $account
Invoke-Sc privs $service "SeChangeNotifyPrivilege/SeCreateGlobalPrivilege/SeIncreaseWorkingSetPrivilege"
Invoke-Sc failure $service "reset=" 86400 "actions=" "restart/60000/restart/60000/restart/60000"
Set-Content -Path $serviceFile -Value $service -Encoding ascii
$sid = (New-Object Security.Principal.NTAccount($account)).Translate([Security.Principal.SecurityIdentifier]).Value
Write-Host "service $service under $account ($sid): privileges cut to SeChangeNotifyPrivilege, SeCreateGlobalPrivilege, SeIncreaseWorkingSetPrivilege; SID type restricted"
# RunnerService.exe writes to the Application log under this source, which
# only an administrator can create.
$source = "ActionsRunnerService"
try {
    if (-not [Diagnostics.EventLog]::SourceExists($source)) { [Diagnostics.EventLog]::CreateEventSource($source, "Application") }
} catch {
    Write-Warning "could not create the event source ${source}: $_"
}

# 7. File access: read and run on -Dir, modify on the directories the
# runner and the jobs write, read and run on the evidence directory.
Invoke-Icacls $Dir /grant "${account}:(OI)(CI)RX" /Q
foreach ($d in @($Work, $diag, $Cache, $Temp, $home_)) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
    Invoke-Icacls $d /grant "${account}:(OI)(CI)M" /T /C /Q
    Write-Host "${d}: $account may modify"
}
if ($Evidence) {
    Invoke-Icacls $Evidence /grant "${account}:(OI)(CI)RX" /T /C /Q
    Write-Host "${Evidence}: $account may read"
}

# 8. Every fixed drive but the system drive: the account may not read,
# write or delete anything there, from the root down. An explicit grant
# below the root (step 7) still wins over this inherited deny. Windows
# applies the new entry to every file of the drive, which takes a while on
# a large one.
$systemDrive = $env:SystemDrive.TrimEnd('\')
$drives = @(Get-CimInstance -ClassName Win32_LogicalDisk -Filter "DriveType=3" | Where-Object { $_.DeviceID -ne $systemDrive } | ForEach-Object { "$($_.DeviceID)\" })
foreach ($root in $drives) {
    Write-Host "${root}: denying $account read, write and delete from the root down (icacls $root /deny ${account}:(OI)(CI)(R,W,D))"
    & icacls.exe $root /deny "${account}:(OI)(CI)(R,W,D)" /C /Q
    # A file Windows cannot update (one in use, or System Volume
    # Information) is reported and skipped; the check below reads the result.
    if ($LASTEXITCODE -ne 0) { Write-Warning "icacls $root reported files it could not change" }
}

# 9. The write check. The script cannot log on as the account, so it reads
# ACLs: each drive root must hold the deny, and no directory or file at the
# top of a drive may grant write access, in an entry of its own, to the
# account or to a group it belongs to; such an entry would come before the
# inherited deny. Entries deeper down are not read.
# WriteData, AppendData, WriteExtendedAttributes, WriteAttributes, Delete,
# DeleteSubdirectoriesAndFiles, WRITE_DAC, WRITE_OWNER, GENERIC_ALL and
# GENERIC_WRITE.
$writeMask = 0x2 -bor 0x4 -bor 0x10 -bor 0x100 -bor 0x10000 -bor 0x40 -bor 0x40000 -bor 0x80000 -bor 0x10000000 -bor 0x40000000
# Everyone, Authenticated Users, Users, SERVICE, ALL SERVICES, the account.
$broad = @("S-1-1-0", "S-1-5-11", "S-1-5-32-545", "S-1-5-6", "S-1-5-80-0", $sid)
$granted = @($Work, $diag, $Cache, $Temp, $home_)
$sidType = [Security.Principal.SecurityIdentifier]
$problems = @()
foreach ($root in $drives) {
    $rules = (Get-Acl -LiteralPath $root).GetAccessRules($true, $false, $sidType)
    $deny = $rules | Where-Object { $_.AccessControlType -eq "Deny" -and $_.IdentityReference.Value -eq $sid }
    if (-not $deny) { $problems += "${root} holds no deny entry for $account" }
    foreach ($item in Get-ChildItem -LiteralPath $root -Force -ErrorAction SilentlyContinue) {
        if ($granted | Where-Object { $_ -ieq $item.FullName }) { continue }
        # Windows' own: the recycle bin lets every user create a folder of
        # their own in it, and System Volume Information admits only SYSTEM.
        if (@('$RECYCLE.BIN', 'System Volume Information') -contains $item.Name) { continue }
        # A file in use (a page file) has no ACL to read.
        try { $a = Get-Acl -LiteralPath $item.FullName } catch { Write-Host "  not read: $($item.FullName) ($($_.Exception.Message))"; continue }
        foreach ($e in $a.GetAccessRules($true, $false, $sidType)) {
            if ($e.AccessControlType -ne "Allow" -or ([int64]$e.FileSystemRights -band $writeMask) -eq 0) { continue }
            if ($broad -contains $e.IdentityReference.Value) {
                $problems += "$($item.FullName) grants $($e.IdentityReference.Value) $($e.FileSystemRights) in an entry of its own"
            }
        }
    }
}
if ($problems) {
    $problems | ForEach-Object { Write-Host "  $_" }
    throw "the service account can still write outside its directories (above); remove those entries and run this again. The service is installed but not started."
}
Write-Host "write check (ACLs read, not a logon as $account): every non-system drive denies $account, and no top-level entry grants it write access"

# 10. The job-started hook. It lives in -Dir, which the account can read but
# not change, and fails every job but the nightly gate on main.
New-Item -ItemType Directory -Force -Path $hooks | Out-Null
$hook = Join-Path $hooks "job-started.ps1"
$hookText = @'
# Runs before every job of this runner (ACTIONS_RUNNER_HOOK_JOB_STARTED) and
# fails it unless it is the nightly workflow of @REPO@ on main, started by
# its schedule or by hand. service.ps1 writes this file; the service account
# can read it but not change it.
$workflow = '@REPO@/.github/workflows/nightly.yml@refs/heads/main'
if ($env:GITHUB_REPOSITORY -cne '@REPO@' -or
    @('schedule', 'workflow_dispatch') -cnotcontains $env:GITHUB_EVENT_NAME -or
    $env:GITHUB_WORKFLOW_REF -cne $workflow) {
    Write-Output "This runner runs only $workflow, on a schedule or by hand; this job is $env:GITHUB_WORKFLOW_REF ($env:GITHUB_EVENT_NAME) of $env:GITHUB_REPOSITORY."
    exit 1
}
exit 0
'@
Set-Content -Path $hook -Value $hookText.Replace("@REPO@", $Repo) -Encoding ascii

# 11. The service's environment: the runner's listener reads .env in -Dir
# when it starts, and every job inherits it. PATH is the machine's, with
# the tools mk's tests need first.
$tools = @(
    (Join-Path $env:ProgramFiles "Go\bin"),
    (Join-Path $env:ProgramFiles "LLVM\bin"),
    (Join-Path $env:ProgramFiles "Git\cmd"),
    (Join-Path $env:ProgramFiles "Git\usr\bin")
)
foreach ($t in $tools[0..1]) { if (-not (Test-Path $t)) { throw "missing ${t}: install Go and LLVM for all users" } }
$machine = [Environment]::GetEnvironmentVariable("Path", "Machine") -split ";" | Where-Object { $_ }
$path = @($tools | Where-Object { Test-Path $_ }) + @($machine | Where-Object { $tools -notcontains $_ })
$envLines = @(
    "ACTIONS_RUNNER_HOOK_JOB_STARTED=$hook",
    "PATH=$($path -join ';')",
    "HOME=$home_",
    "GOCACHE=$(Join-Path $Cache 'go-build')",
    "GOPATH=$(Join-Path $Cache 'gopath')",
    "MK_CACHE=$(Join-Path $Cache 'mk')",
    "TMP=$Temp",
    "TEMP=$Temp"
)
if ($Evidence) { $envLines += "MK_EVIDENCE=$Evidence" }
Set-Content -Path (Join-Path $Dir ".env") -Value $envLines -Encoding ascii

# 12. Below-normal CPU priority for the runner's processes. A process that
# does not ask for a priority class inherits a below-normal parent's, so
# every build and test a job starts runs below normal too. These entries
# apply to every process of these names on the machine.
$ifeo = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Image File Execution Options"
foreach ($exe in @("RunnerService.exe", "Runner.Listener.exe", "Runner.Worker.exe")) {
    $key = Join-Path $ifeo "$exe\PerfOptions"
    New-Item -Path $key -Force | Out-Null
    # 5 is BELOW_NORMAL_PRIORITY_CLASS.
    New-ItemProperty -Path $key -Name CpuPriorityClass -PropertyType DWord -Value 5 -Force | Out-Null
}

# 13. Start it, now and at every boot.
Invoke-Sc config $service "start=" delayed-auto
Start-Service -Name $service
Get-Service -Name $service | Format-Table -AutoSize Name, Status, StartType
Write-Host "Runner $Name runs as the service $service under $account; check it under Settings > Actions > Runners."
Write-Host "To see a job's privileges, run whoami /priv in a step of the nightly workflow."
