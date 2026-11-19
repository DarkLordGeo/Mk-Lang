// Why: the client, the server and the language registration only meet
// inside a real VS Code, so they need one test that runs there.
// What: launches the installed VS Code (no download) with this folder as a
// development extension and the repository as the workspace, runs suite.js.
// How: a throwaway user-data and extensions directory, so no profile is
// touched; mk from MK_EXE or mk(.exe) at the repository root, VS Code from
// VSCODE_EXE or `code` on PATH. Run: npm run test:vscode.

'use strict';

const cp = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const ext = path.resolve(__dirname, '..', '..');
const repo = path.resolve(ext, '..', '..');

function codeExe() {
  if (process.env.VSCODE_EXE) return process.env.VSCODE_EXE;
  const exe = process.platform === 'win32' ? 'Code.exe' : 'code';
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    if (process.platform === 'win32' && fs.existsSync(path.join(dir, 'code.cmd'))) {
      return path.join(dir, '..', exe); // <install>/bin/code.cmd -> <install>/Code.exe
    }
    if (process.platform !== 'win32' && fs.existsSync(path.join(dir, 'code'))) return path.join(dir, 'code');
  }
  throw new Error('VS Code not found: set VSCODE_EXE');
}

const mk = process.env.MK_EXE || path.join(repo, process.platform === 'win32' ? 'mk.exe' : 'mk');
if (!fs.existsSync(mk)) {
  console.error(`no mk at ${mk}: go build -o mk.exe ./cmd/mk at the repository root, or set MK_EXE`);
  process.exit(2);
}
const tmp = fs.mkdtempSync(path.join(process.env.MK_VSCODE_TMP || os.tmpdir(), 'mk-vscode-'));
const userDir = path.join(tmp, 'user');
fs.mkdirSync(path.join(userDir, 'User'), { recursive: true });
fs.writeFileSync(path.join(userDir, 'User', 'settings.json'), JSON.stringify({
  'mk.server.path': mk,
  'mk.trace.server': 'messages',
  'mk.server.log': path.join(tmp, 'lsp.log'),
  // Never the default <package>/.mk-cache: one under tests/ or std/
  // breaks std's own tests.
  'mk.server.cache': path.join(tmp, 'cache'),
  'security.workspace.trust.enabled': false,
  'telemetry.telemetryLevel': 'off',
  'update.mode': 'none',
}, null, 2));
const reportPath = path.join(tmp, 'report.txt');
const args = [
  repo,
  `--extensionDevelopmentPath=${ext}`,
  `--extensionTestsPath=${path.join(__dirname, 'suite.js')}`,
  `--user-data-dir=${userDir}`,
  `--extensions-dir=${path.join(tmp, 'extensions')}`,
  '--disable-workspace-trust',
  '--skip-welcome',
  '--skip-release-notes',
  '--new-window',
];
const env = { ...process.env, MK_VSCODE_REPORT: reportPath };
delete env.ELECTRON_RUN_AS_NODE;
const r = cp.spawnSync(codeExe(), args, { env, stdio: 'ignore', timeout: 240000 });
const lsp = path.join(tmp, 'lsp.log');
if (fs.existsSync(lsp)) console.log('server log (first 40 lines):\n' + fs.readFileSync(lsp, 'utf8').split('\n').slice(0, 40).join('\n'));
if (fs.existsSync(reportPath)) process.stdout.write(fs.readFileSync(reportPath, 'utf8'));
console.log(`VS Code exited ${r.status}${r.error ? ' (' + r.error.message + ')' : ''}; profile in ${tmp}`);
process.exit(r.status === 0 ? 0 : 1);
