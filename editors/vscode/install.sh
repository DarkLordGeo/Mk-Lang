#!/bin/sh
# Why: installing the extension from a source checkout takes three commands;
# this runs them in order and stops at the first failure.
# What: packages editors/vscode into mk-lang.vsix and installs it into VS Code.
# How: npm ci (exact versions from package-lock.json), vsce package, then
# code --install-extension --force. Needs Node 18 or later and `code` on PATH.
# Run: sh editors/vscode/install.sh (from anywhere).

set -eu
cd "$(dirname "$0")"
npm ci
npx vsce package --out mk-lang.vsix
code --install-extension mk-lang.vsix --force
echo 'Installed. Reload VS Code, and put mk on PATH or set mk.server.path.'
