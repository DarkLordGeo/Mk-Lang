# Why: many wsl.exe clients at once (parallel test binaries, parallel
# jobs) hang the WSL service until a reboot, so at most one command at a
# time may drive Linux runs through wsl.exe.
#
# What: runs one command while holding the machine-wide WSL lock.
#   pwsh -NoProfile -File tools/wsl-lock.ps1 go test -p 1 ./internal/corpus
#
# How: the lock is a named mutex, so a holder that dies releases it: the next
# waiter gets AbandonedMutexException and owns the lock, and with no waiter
# the holder file it leaves (C:\ProgramData\MkWSL) says the same to the
# next holder. Its wsl.exe clients may still be running then, so after an
# abandoned acquire the new
# holder waits until no wsl process is left (polling every 10 s, logging
# each minute), for at most MK_WSL_LOCK_DRAIN seconds (default 900), and
# then goes on with a warning.
#
# The command runs inside a Windows job object with
# JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE that holds this script's own process:
# every process the command starts is in the job, and when this script
# ends, however it ends (killed included), the system closes the job's
# handle and kills the whole tree. The command sees MK_WSL_LOCKED=1, which
# the tests' MK_LINUX guards ask for. Waits are logged every minute. Exit
# code is the command's.

if ($args.Count -eq 0) {
    Write-Error "usage: wsl-lock.ps1 <command> [args...]"
    exit 2
}

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class MkWslLockJob {
    [StructLayout(LayoutKind.Sequential)]
    struct BasicLimit {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
        public uint PriorityClass;
        public uint SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct IoCounters {
        public ulong ReadOperationCount, WriteOperationCount, OtherOperationCount;
        public ulong ReadTransferCount, WriteTransferCount, OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct ExtendedLimit {
        public BasicLimit Basic;
        public IoCounters Io;
        public UIntPtr ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed;
    }

    const uint KillOnJobClose = 0x2000;
    const int ExtendedLimitInformation = 9;

    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateJobObject(IntPtr attributes, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int infoClass, ref ExtendedLimit info, uint length);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll")]
    static extern IntPtr GetCurrentProcess();

    // The job's handle; never closed here, so it lives until this process
    // ends, and the system's close then kills every process in the job.
    static IntPtr job;

    public static void HoldSelf() {
        job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero) {
            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "CreateJobObject");
        }
        var info = new ExtendedLimit();
        info.Basic.LimitFlags = KillOnJobClose;
        if (!SetInformationJobObject(job, ExtendedLimitInformation, ref info, (uint)Marshal.SizeOf(typeof(ExtendedLimit)))) {
            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "SetInformationJobObject");
        }
        if (!AssignProcessToJobObject(job, GetCurrentProcess())) {
            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "AssignProcessToJobObject");
        }
    }
}
'@

# MK_WSL_LOCK_NAME names another mutex, only to test this script without
# touching the real lock.
$name = 'Global\MkWSL'
if ($env:MK_WSL_LOCK_NAME) { $name = $env:MK_WSL_LOCK_NAME }
$m = [System.Threading.Mutex]::new($false, $name)
$owned = $false
$abandoned = $false
try {
    $waited = 0
    while (-not $owned) {
        try {
            $owned = $m.WaitOne([TimeSpan]::FromMinutes(1))
        } catch [System.Threading.AbandonedMutexException] {
            $owned = $true
            $abandoned = $true
        }
        if (-not $owned) {
            $waited++
            Write-Host "wsl-lock: waiting for another WSL user ($waited min)"
        }
    }

    # A holder that dies with no waiter leaves no abandoned mutex (the
    # system frees the named mutex with its last handle), but it does
    # leave its holder file, which a clean exit removes. Holding the lock,
    # any holder file found is a dead holder's.
    $stateDir = Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'MkWSL'
    $null = New-Item -ItemType Directory -Force $stateDir
    $holder = Join-Path $stateDir (($name -replace '[^A-Za-z0-9_-]', '_') + '.holder')
    # The dead holder's clients are the wsl processes started after it took
    # the lock; wsl processes older than that (terminals, the WSL service's
    # own long-lived clients) are not its and are never waited for.
    $since = $null
    if (Test-Path $holder) {
        $prev = Get-Content -Raw $holder
        Write-Host "wsl-lock: a previous holder ended without releasing the lock: $prev"
        $abandoned = $true
        if ($prev -match 'since (\S+):') { try { $since = [datetime]::Parse($Matches[1]) } catch { $since = $null } }
    }
    Set-Content -Path $holder -Value "pid $PID since $(Get-Date -Format o): $($args -join ' ')"

    if ($abandoned) {
        # The previous holder died holding the lock; its wsl.exe clients
        # may live on. Wait for every wsl process to end, within a bound.
        $limit = 900
        if ($env:MK_WSL_LOCK_DRAIN) { $limit = [int]$env:MK_WSL_LOCK_DRAIN }
        Write-Host "wsl-lock: the previous holder died holding the lock; waiting up to $limit s for the wsl.exe clients started since $since to end"
        $start = Get-Date
        $logged = 0
        while ($true) {
            $left = @(Get-Process wsl -ErrorAction SilentlyContinue | Where-Object { -not $since -or ($_.StartTime -ge $since) })
            if ($left.Count -eq 0) {
                Write-Host "wsl-lock: no wsl process left"
                break
            }
            $secs = ((Get-Date) - $start).TotalSeconds
            if ($secs -ge $limit) {
                Write-Warning "wsl-lock: $($left.Count) wsl processes still running after $limit s (ids $($left.Id -join ', ')); going on"
                break
            }
            if ([int]($secs / 60) -gt $logged) {
                $logged = [int]($secs / 60)
                Write-Host "wsl-lock: $($left.Count) wsl processes still running ($logged min)"
            }
            Start-Sleep -Seconds ([Math]::Min(10, [Math]::Max(1, $limit - $secs)))
        }
    }

    try {
        [MkWslLockJob]::HoldSelf()
    } catch {
        Write-Warning "wsl-lock: no kill-on-close job ($($_.Exception.Message)); killing this script will not kill the command's tree"
    }
    $env:MK_WSL_LOCKED = '1'
    $cmd = $args[0]
    $rest = @()
    if ($args.Count -gt 1) { $rest = $args[1..($args.Count - 1)] }
    & $cmd @rest
    $code = $LASTEXITCODE
    exit $code
} finally {
    if ($owned) {
        if ($holder) { Remove-Item -Force -ErrorAction SilentlyContinue $holder }
        $m.ReleaseMutex()
    }
    $m.Dispose()
}
