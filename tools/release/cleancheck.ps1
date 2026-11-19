# Why: the Windows release archive must build and run programs on a machine
# with no Visual Studio, Windows SDK or LLVM, and only a clean machine shows
# that nothing outside the bundle is used.
#
# What: the clean-machine check of a Windows release archive; it writes a
# Markdown transcript and exits 0 when every step passed.
#
# How: it records what is installed, unpacks the archive, and runs mk in a
# child process whose environment is cleared down to the system directories
# and the bundle's bin (see the help below).

<#
.SYNOPSIS
The clean-machine check of a Windows release archive.

.DESCRIPTION
Run it in Windows Sandbox or a fresh virtual machine, with the Windows
archive copied in:

    powershell -NoProfile -ExecutionPolicy Bypass -File cleancheck.ps1 -Archive C:\in\mk-0.1.0-x86_64-windows.zip

It records that no Visual Studio, Windows SDK or LLVM is installed (their
registry keys and directories are absent), unpacks the archive, and then,
in a child environment whose PATH holds only the Windows system directories
and the archive's bin, with a home, TEMP and LOCALAPPDATA of its own:
records that no compiler, linker or SDK tool is reachable by PATH; runs
`mk version`, which must name the bundle's LLVM ("(bundle)"); builds and
runs hello and a package with a C source and a C import (whose C includes
<stdint.h> from the bundle's clang headers); and builds both for
x86_64-linux. The two Linux executables are left in <Work>\linux for the
Linux leaves to run. The transcript (Markdown) goes to -Transcript
(default <Work>\transcript.md). Exit 0 when every step passed, 1 otherwise.

-Local is the synthetic local run on a developer's machine, which has
Visual Studio, an SDK or LLVM installed: their presence is recorded and not
a failure, and the child's reduced PATH and environment show that mk
reaches none of them (it uses the bundle). A real clean-machine run passes
no -Local.

Windows PowerShell 5.1 or later; ASCII only; no module beyond .NET.
#>
param(
    [Parameter(Mandatory = $true)][string]$Archive,
    [string]$Work = '',
    [string]$Transcript = '',
    [switch]$Local
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

if ($Work -eq '') { $Work = Join-Path $env:TEMP 'mk-cleancheck' }
$Work = [IO.Path]::GetFullPath($Work)
if ($Transcript -eq '') { $Transcript = Join-Path $Work 'transcript.md' }
$Archive = [IO.Path]::GetFullPath($Archive)

$script:lines = New-Object System.Collections.Generic.List[string]
$script:failed = 0
function Out-Line([string]$s) { $script:lines.Add($s); Write-Host $s }
function Out-Fail([string]$s) { $script:failed++; Out-Line ("- **FAIL** " + $s) }
function Out-Pass([string]$s) { Out-Line ("- ok: " + $s) }

if (Test-Path -LiteralPath $Work) {
    if ((Get-ChildItem -LiteralPath $Work -Force | Measure-Object).Count -ne 0) {
        throw "the work directory $Work is not empty; name a new one with -Work"
    }
}
New-Item -ItemType Directory -Force -Path $Work | Out-Null

$mode = 'clean machine'
if ($Local) { $mode = 'synthetic local run (-Local): presence is recorded, not a failure' }
Out-Line '# Clean-machine check (tools/release/cleancheck.ps1)'
Out-Line ''
Out-Line ('- mode: ' + $mode)
Out-Line ('- date: ' + (Get-Date).ToString('yyyy-MM-dd HH:mm:ss zzz'))
Out-Line ('- Windows: ' + [Environment]::OSVersion.VersionString + ', PowerShell ' + $PSVersionTable.PSVersion.ToString())
$sha = [BitConverter]::ToString((New-Object Security.Cryptography.SHA256Managed).ComputeHash([IO.File]::ReadAllBytes($Archive))).Replace('-', '').ToLowerInvariant()
Out-Line ('- archive: ' + [IO.Path]::GetFileName($Archive) + ', ' + (Get-Item -LiteralPath $Archive).Length + ' bytes, SHA-256 ' + $sha)
Out-Line ''

# 1. What is installed.
Out-Line '## Installed toolchains (registry keys and directories)'
Out-Line ''
$present = @()
$keys = @(
    'HKLM:\SOFTWARE\Microsoft\VisualStudio\SxS\VS7',
    'HKLM:\SOFTWARE\WOW6432Node\Microsoft\VisualStudio\SxS\VS7',
    'HKLM:\SOFTWARE\Microsoft\Windows Kits\Installed Roots',
    'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows Kits\Installed Roots',
    'HKLM:\SOFTWARE\LLVM\LLVM',
    'HKLM:\SOFTWARE\WOW6432Node\LLVM\LLVM'
)
foreach ($k in $keys) {
    if (Test-Path -LiteralPath $k) { $present += $k; Out-Line ('- present: ' + $k) } else { Out-Line ('- absent: ' + $k) }
}
$pf86 = [Environment]::GetFolderPath('ProgramFilesX86')
$dirs = @(
    (Join-Path $env:ProgramFiles 'LLVM'),
    (Join-Path $env:ProgramFiles 'Microsoft Visual Studio'),
    (Join-Path $pf86 'Microsoft Visual Studio'),
    (Join-Path $pf86 'Windows Kits'),
    (Join-Path $env:ProgramFiles 'Windows Kits')
)
foreach ($d in $dirs) {
    if (Test-Path -LiteralPath $d) { $present += $d; Out-Line ('- present: ' + $d) } else { Out-Line ('- absent: ' + $d) }
}
if ($present.Count -eq 0) {
    Out-Pass 'no Visual Studio, Windows SDK or LLVM is installed'
} elseif ($Local) {
    Out-Line ('- recorded (-Local): ' + $present.Count + ' installed location(s) above; the child below must not reach them')
} else {
    Out-Fail ('not a clean machine: ' + ($present -join '; '))
}
Out-Line ''

# 2. Unpack.
Out-Line '## Unpack'
Out-Line ''
Add-Type -AssemblyName System.IO.Compression.FileSystem
$unpack = Join-Path $Work 'unpacked'
[IO.Compression.ZipFile]::ExtractToDirectory($Archive, $unpack)
$top = @(Get-ChildItem -LiteralPath $unpack -Directory)
if ($top.Count -ne 1) { throw "the archive holds $($top.Count) top directories, want one" }
$bundle = $top[0].FullName
$mk = Join-Path $bundle 'bin\mk.exe'
Out-Line ('- unpacked to ' + $bundle)
foreach ($f in @('bin\mk.exe', 'llvm\bin\clang.exe', 'llvm\bin\lld.exe', 'RELEASE.txt', 'CHANGELOG.md', 'LICENSE', 'LICENSE-LLVM.txt', 'doc\std-api.md', 'seed\first-party.txt')) {
    if (Test-Path -LiteralPath (Join-Path $bundle $f)) { Out-Pass $f } else { Out-Fail ('missing ' + $f) }
}
Out-Line ''

# 3. The child environment.
$sys = [Environment]::GetFolderPath('System')
$win = $env:SystemRoot
$childPath = @($sys, (Join-Path $sys 'Wbem'), (Join-Path $sys 'WindowsPowerShell\v1.0'), $win, (Join-Path $bundle 'bin')) -join ';'
$prof = Join-Path $Work 'profile'
$childEnv = @{
    'PATH' = $childPath; 'SystemRoot' = $win; 'windir' = $win; 'ComSpec' = (Join-Path $sys 'cmd.exe');
    'PATHEXT' = '.COM;.EXE;.BAT;.CMD'; 'SystemDrive' = $env:SystemDrive;
    'NUMBER_OF_PROCESSORS' = $env:NUMBER_OF_PROCESSORS; 'PROCESSOR_ARCHITECTURE' = $env:PROCESSOR_ARCHITECTURE;
    'TEMP' = (Join-Path $prof 'temp'); 'TMP' = (Join-Path $prof 'temp');
    'USERPROFILE' = (Join-Path $prof 'home'); 'HOME' = (Join-Path $prof 'home');
    'LOCALAPPDATA' = (Join-Path $prof 'localappdata'); 'APPDATA' = (Join-Path $prof 'appdata')
}
foreach ($v in @('TEMP', 'USERPROFILE', 'LOCALAPPDATA', 'APPDATA')) { New-Item -ItemType Directory -Force -Path $childEnv[$v] | Out-Null }

function Invoke-Child([string]$exe, [string[]]$argv, [string]$dir) {
    $psi = New-Object Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $quoted = foreach ($a in $argv) { if ($a -match '[\s"]') { '"' + ($a -replace '"', '\"') + '"' } else { $a } }
    $psi.Arguments = ($quoted -join ' ')
    $psi.WorkingDirectory = $dir
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.EnvironmentVariables.Clear()
    foreach ($k in $childEnv.Keys) { $psi.EnvironmentVariables[$k] = $childEnv[$k] }
    $p = [Diagnostics.Process]::Start($psi)
    $errTask = $p.StandardError.ReadToEndAsync()
    $out = $p.StandardOutput.ReadToEnd()
    $p.WaitForExit()
    return @{ Code = $p.ExitCode; Out = ($out + $errTask.Result).TrimEnd() }
}

function Step([string]$what, [string]$exe, [string[]]$argv, [string]$dir, [string]$want) {
    $r = Invoke-Child $exe $argv $dir
    Out-Line ('### ' + $what)
    Out-Line ''
    Out-Line ('`' + [IO.Path]::GetFileName($exe) + ' ' + ($argv -join ' ') + '` in `' + $dir + '`: exit ' + $r.Code)
    Out-Line ''
    Out-Line '```'
    if ($r.Out -ne '') { foreach ($l in ($r.Out -split "`r?`n")) { Out-Line $l } }
    Out-Line '```'
    Out-Line ''
    if ($r.Code -ne 0) { Out-Fail ($what + ': exit ' + $r.Code) }
    elseif ($want -ne '' -and -not $r.Out.Contains($want)) { Out-Fail ($what + ': the output lacks "' + $want + '"') }
    else { Out-Pass $what }
    Out-Line ''
    return $r
}

Out-Line '## The child environment'
Out-Line ''
Out-Line ('- PATH: `' + $childPath + '`')
Out-Line ('- every other variable cleared but ' + (($childEnv.Keys | Where-Object { $_ -ne 'PATH' } | Sort-Object) -join ', ') + '; no MK_*, INCLUDE, LIB or VS variables; the home, TEMP, APPDATA and LOCALAPPDATA are under ' + $prof)
foreach ($tool in @('clang', 'clang-cl', 'lld-link', 'ld.lld', 'lld', 'llvm-lib', 'cl', 'link', 'lib', 'rc', 'mt', 'vswhere', 'gcc', 'cc')) {
    $r = Invoke-Child (Join-Path $sys 'where.exe') @('/Q', $tool) $Work
    if ($r.Code -eq 0) { Out-Fail ($tool + ' is reachable by the child''s PATH') } else { Out-Pass ($tool + ' is not reachable by the child''s PATH') }
}
Out-Line ''

# 4. mk, hello, the C import, the Linux executables.
Out-Line '## Builds'
Out-Line ''
$v = Step 'mk version' $mk @('version') $Work '(bundle)'

$hello = Join-Path $Work 'hello'
New-Item -ItemType Directory -Force -Path $hello | Out-Null
[IO.File]::WriteAllText((Join-Path $hello 'main.mk'), "export Main(sys: &System): WriteError!void {`n    try sys.Print(""hello from the bundle\n"", ())`n}`n")
$null = Step 'build hello' $mk @('build', '-o', 'hello.exe', 'main.mk') $hello ''
$null = Step 'run hello' (Join-Path $hello 'hello.exe') @() $hello 'hello from the bundle'

$cimp = Join-Path $Work 'cimport'
foreach ($d in @('src', 'csrc', 'include')) { New-Item -ItemType Directory -Force -Path (Join-Path $cimp $d) | Out-Null }
[IO.File]::WriteAllText((Join-Path $cimp 'pkg.mk'), @"
const Package = pkg.Package(
    name: "cimport",
    bins: [pkg.Bin(name: "cimport", root: "src", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "csrc/two.c", flags: [])],
    c_imports: [pkg.CImport(name: "two", headers: ["include/two.h"], defines: [], macros: [], vendor: false)],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["src"],
    links: [],
)
"@.Replace("`r`n", "`n"))
[IO.File]::WriteAllText((Join-Path $cimp 'include\two.h'), "int two(void);`n")
[IO.File]::WriteAllText((Join-Path $cimp 'csrc\two.c'), "#include <stdint.h>`n#include ""../include/two.h""`nint two(void) { int32_t n = 2; return (int)n; }`n")
[IO.File]::WriteAllText((Join-Path $cimp 'src\main.mk'), "import c.two`n`nexport Main(sys: &System): WriteError!void {`n    var n: i32 = 0`n    unsafe ""two returns a constant"" {`n        n = two.two()`n    }`n    try sys.Print(""C import says {n}\n"", (n))`n}`n")
$null = Step 'build the C import package' $mk @('build', '-o', 'cimport.exe', '.') $cimp ''
$null = Step 'run the C import package' (Join-Path $cimp 'cimport.exe') @() $cimp 'C import says 2'

$linux = Join-Path $Work 'linux'
New-Item -ItemType Directory -Force -Path $linux | Out-Null
$null = Step 'build hello for x86_64-linux' $mk @('build', '--target', 'x86_64-linux', '-o', (Join-Path $linux 'hello'), 'main.mk') $hello ''
$null = Step 'build the C import package for x86_64-linux' $mk @('build', '--target', 'x86_64-linux', '-o', (Join-Path $linux 'cimport'), '.') $cimp ''
foreach ($f in @('hello', 'cimport')) {
    $p = Join-Path $linux $f
    if ((Test-Path -LiteralPath $p) -and ([IO.File]::ReadAllBytes($p)[0..3] -join ',') -eq '127,69,76,70') {
        Out-Pass ($f + ' for x86_64-linux is an ELF file (run it under the Linux leaves: hello prints "hello from the bundle", cimport "C import says 2")')
    } else { Out-Fail ($f + ' for x86_64-linux is missing or not ELF') }
}
Out-Line ''
Out-Line '## Result'
Out-Line ''
if ($script:failed -eq 0) { Out-Line '- **PASS**: every step passed' } else { Out-Line ('- **FAIL**: ' + $script:failed + ' step(s) failed') }
[IO.File]::WriteAllText($Transcript, (($script:lines -join "`n") + "`n"))
if ($script:failed -ne 0) { exit 1 }
exit 0
