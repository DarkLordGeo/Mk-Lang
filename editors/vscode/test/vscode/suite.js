// Why: hover, definition and implementation must work through VS Code's own
// providers, not only in the server's unit tests.
// What: run(), which opens real Mk files and checks hover, definition,
// implementation, inlay hints, document symbols and diagnostics.
// How: runs in the extension host started by run.js, retries each request
// while the server warms up, appends each result to MK_VSCODE_REPORT and
// throws on a failed check.

'use strict';

const fs = require('fs');
const path = require('path');
const vscode = require('vscode');

const repo = path.resolve(__dirname, '..', '..', '..', '..');
const report = [];
const reportFile = () => process.env.MK_VSCODE_REPORT || path.join(__dirname, 'report.txt');
const failed = [];

function check(ok, what) {
  report.push(`${ok ? 'ok  ' : 'FAIL'} ${what}`);
  fs.appendFileSync(reportFile(), report[report.length - 1] + '\n');
  if (!ok) failed.push(what);
}

function hoverText(hs) {
  return (hs || [])
    .flatMap((h) => h.contents)
    .map((c) => (typeof c === 'string' ? c : c.value))
    .join('\n');
}

async function retry(fn, ok, ms = 15000) {
  const end = Date.now() + ms;
  let v;
  for (;;) {
    v = await fn();
    if (ok(v) || Date.now() > end) return v;
    await new Promise((r) => setTimeout(r, 300));
  }
}

function posOf(doc, needle, delta = 0) {
  const i = doc.getText().indexOf(needle);
  if (i < 0) throw new Error(`${needle} not in ${doc.uri.fsPath}`);
  return doc.positionAt(i + delta);
}

function locPath(l) {
  const uri = l.targetUri || l.uri;
  return uri.fsPath;
}

async function probe(rel, cases) {
  const doc = await vscode.workspace.openTextDocument(path.join(repo, rel));
  await vscode.window.showTextDocument(doc);
  check(doc.languageId === 'mk', `${rel}: language id ${doc.languageId}`);
  for (const c of cases) {
    const p = posOf(doc, c.at, c.off || 0);
    if (c.hover) {
      const hs = await retry(() => vscode.commands.executeCommand('vscode.executeHoverProvider', doc.uri, p), (h) => hoverText(h).includes(c.hover));
      const t = hoverText(hs);
      check(t.includes(c.hover), `${rel}: hover at ${JSON.stringify(c.at)} has ${JSON.stringify(c.hover)}: ${JSON.stringify(t.slice(0, 160))}`);
    }
    if (c.def) {
      const ls = await retry(() => vscode.commands.executeCommand('vscode.executeDefinitionProvider', doc.uri, p), (l) => l && l.length > 0);
      const got = (ls || []).map(locPath);
      check(got.some((g) => g.replace(/\\/g, '/').endsWith(c.def)), `${rel}: definition at ${JSON.stringify(c.at)} in ${c.def}: ${got.join(', ')}`);
    }
    if (c.impl) {
      // "Go to implementation", as the user clicks it.
      const ls = await retry(() => vscode.commands.executeCommand('vscode.executeImplementationProvider', doc.uri, p), (l) => l && l.length > 0);
      const got = (ls || []).map((l) => `${locPath(l)}:${(l.targetRange || l.range).start.line + 1}`);
      check(got.some((g) => g.replace(/\\/g, '/').endsWith(c.impl)), `${rel}: implementation at ${JSON.stringify(c.at)} in ${c.impl}: ${got.join(', ')}`);
    }
  }
  return doc;
}

async function run() {
  const ext = vscode.extensions.all.find((e) => e.packageJSON && e.packageJSON.name === 'mk-lang');
  check(!!ext, 'extension mk-lang loaded');
  const hello = await probe('tests/programs/hello.mk', [
    { at: 'Print(', hover: 'Print[A: Fields[Format] + Plain]', def: 'std/core/core_fmt.mk' },
    { at: 'System)', hover: 'struct System', def: 'std/core/io_system.mk' },
    { at: 'sys.Stdout', hover: 'sys: &System', def: 'tests/programs/hello.mk' },
  ]);
  const syms = await retry(() => vscode.commands.executeCommand('vscode.executeDocumentSymbolProvider', hello.uri), (s) => s && s.length > 0);
  check((syms || []).some((s) => s.name === 'Main'), `hello.mk: document symbols ${(syms || []).map((s) => s.name).join(' ')}`);
  const diags = vscode.languages.getDiagnostics(hello.uri);
  check(diags.length === 0, `hello.mk: ${diags.length} diagnostics`);

  // A std file, opened as browsing does after a definition.
  const order = await probe('std/atomic/order.mk', [
    { at: 'raw.FenceOrd', off: 4, hover: 'FenceOrd', def: 'std/raw/raw.mk' },
    { at: 'o.CanFence()', off: 2, hover: 'CanFence', def: 'std/atomic/order.mk' },
    { at: 'Fence(const o: Order)', off: 16, hover: 'struct Order', def: 'std/atomic/order.mk' },
  ]);
  await new Promise((r) => setTimeout(r, 3000));
  const stdDiags = vscode.languages.getDiagnostics().filter(([u, ds]) => ds.length > 0 && u.fsPath.toLowerCase().startsWith(path.join(repo, 'std').toLowerCase()));
  check(stdDiags.length === 0, `std files with diagnostics after opening order.mk: ${stdDiags.map(([u, ds]) => path.relative(repo, u.fsPath) + ' ' + ds.length).join(', ') || 'none'}`);
  check(vscode.languages.getDiagnostics(order.uri).length === 0, 'order.mk: no diagnostics');

  // std/compress: Go to implementation on the free function Run, the deep
  // hover of a std type, an import binding.
  await probe('std/compress/compress.mk', [
    { at: 'Run(d, data, out)', hover: 'Run(d: mut Dec, src: []u8, out: mut []u8): int', def: 'std/compress/inflate.mk', impl: 'std/compress/inflate.mk:130' },
  ]);
  // Every token of two lines of compress.mk.
  const l75 = 'fail Corrupt(offset: d.c.eoff)';
  const l82 = 'cap := out.Len()';
  await probe('std/compress/compress.mk', [
    { at: l75, off: 0, hover: 'Error = Corrupt | Truncated | TooLarge | OutOfMemory' },
    { at: l75, off: 5, hover: '**In the error sets**', def: 'std/compress/compress.mk' },
    { at: l75, off: 13, hover: '(field) offset: int', def: 'std/compress/compress.mk' },
    { at: l75, off: 21, hover: '(param) d: mut Dec', def: 'std/compress/compress.mk' },
    { at: l75, off: 23, hover: '(field) c: Cur', def: 'std/compress/inflate.mk', impl: 'std/compress/inflate.mk:61' },
    { at: l75, off: 25, hover: '(field) eoff: int', def: 'std/compress/inflate.mk', impl: 'std/compress/inflate.mk:51' },
    { at: l82, off: 0, hover: '(local) cap: int' },
    { at: l82, off: 7, hover: 'Holds a loan from `buf`' },
    { at: l82, off: 11, hover: 'Here on `out: mut []u8`', def: 'std/core/core_slice.mk' },
  ]);
  {
    const doc = await vscode.workspace.openTextDocument(path.join(repo, 'std/compress/compress.mk'));
    const p = posOf(doc, l82);
    const hints = await retry(() => vscode.commands.executeCommand('vscode.executeInlayHintProvider', doc.uri, new vscode.Range(p.line, 0, p.line + 1, 0)), (h) => h && h.length > 0);
    const labels = (hints || []).map((h) => (typeof h.label === 'string' ? h.label : h.label.map((x) => x.value).join('')));
    check(labels.some((l) => l.includes('int')), `compress.mk:82: inlay hint on cap: ${labels.join(', ')}`);
  }
  await probe('std/compress/inflater.mk', [
    { at: 'export view struct Inflater', off: 19, hover: '**Layout:** size' },
  ]);
  await probe('std/compress/inflate.mk', [
    { at: 'd: mut Dec', off: 7, hover: 'lit: [2342]u32', def: 'std/compress/inflate.mk', impl: 'std/compress/inflate.mk:58' },
  ]);
  await probe('std/compress/deflate.mk', [
    { at: 'raw.Ctz64', hover: 'import std.raw', def: 'std/raw/raw.mk' },
    { at: 'raw.Ctz64', off: 4, hover: 'Ctz64', def: 'std/raw/raw.mk' },
  ]);

  fs.writeFileSync(process.env.MK_VSCODE_REPORT || path.join(__dirname, 'report.txt'), report.join('\n') + '\n');
  if (failed.length) throw new Error(`${failed.length} checks failed:\n${failed.join('\n')}`);
}

module.exports = { run };
