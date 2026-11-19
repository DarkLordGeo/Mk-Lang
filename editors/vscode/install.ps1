# Why: installing the extension from a source checkout takes three commands;
# this runs them in order and stops at the first failure.
# What: packages editors/vscode into mk-lang.vsix and installs it into VS Code.
# How: npm ci (exact versions from package-lock.json), vsce package, then
# code --install-extension --force. Needs Node 18 or later and `code` on PATH.
# Run: pwsh editors/vscode/install.ps1 (from anywhere).

$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    npm ci
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    # vsce runs `npm` through cmd.exe, which cannot read a PATH longer than
    # 8191 characters; give it a short one for this step.
    $path = $env:Path
    if ($path.Length -gt 8000) {
        $env:Path = (Split-Path (Get-Command node).Source) + ';' + (Join-Path $env:SystemRoot 'system32')
    }
    try {
        npx vsce package --out mk-lang.vsix
        if ($LASTEXITCODE -ne 0) { throw 'vsce package failed' }
    } finally {
        $env:Path = $path
    }
    code --install-extension mk-lang.vsix --force
    if ($LASTEXITCODE -ne 0) { throw 'code --install-extension failed' }
    Write-Host 'Installed. Reload VS Code, and put mk on PATH or set mk.server.path.'
} finally {
    Pop-Location
}
