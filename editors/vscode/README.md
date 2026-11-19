# Mk for VS Code

Support for the Mk language in VS Code: syntax highlighting, snippets, comment and bracket settings, and a client for `mk lsp`, the language server built into the `mk` command. Diagnostics, hover, completion, navigation, rename, formatting and quick fixes come from the server, so they match what `mk check` reports.

Install the `.vsix` attached to each GitHub release with `code --install-extension <file>.vsix`, or build it from a checkout with `sh install.sh` (Windows: `pwsh install.ps1`) in `editors/vscode`. The extension needs `mk` on `PATH`, in the workspace folder, or in the `mk.server.path` setting.

[doc/editors.md](https://github.com/zerodayokay/Mk-Lang/blob/main/doc/editors.md#vs-code) covers installing, finding the server, every setting and the known limits, and the setup for other editors.
