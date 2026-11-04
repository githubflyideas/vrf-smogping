#!/usr/bin/env node
'use strict';
// Genuine native capture. Requires Go and Playwright with Microsoft Edge installed.
// Override GO, PLAYWRIGHT_MODULE, CAPTURE_SCRATCH when running on another host.
// No API mocks, history seeding, DOM edits, or remote probe targets are used.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const crypto = require('node:crypto');
const { spawn, execFileSync } = require('node:child_process');
const { once } = require('node:events');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '..');
const scratch = process.env.CAPTURE_SCRATCH || process.env.TMPDIR || path.join(os.tmpdir(), 'vrf-ping-capture');
const go = process.env.GO || path.resolve(root, '../toolchains/go/bin', process.platform === 'win32' ? 'go.exe' : 'go');
const playwright = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), 'AppData/Local/hermes/hermes-agent/node_modules/playwright'));
const version = '3.3.1-preview';
const binary = path.join(scratch, 'vrf-ping-native-screenshot' + (process.platform === 'win32' ? '.exe' : ''));
const image = path.join(root, 'docs/images/vrf-ping-graph.png');
const manifestFile = path.join(root, 'docs/images/vrf-ping-graph.json');
const sha = value => crypto.createHash('sha256').update(value).digest('hex');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function sourceFingerprint() {
  const files = fs.readdirSync(root).filter(f => f.endsWith('.go') && !f.endsWith('_test.go'));
  function walk(dir) {
    for (const ent of fs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
      const rel = dir + '/' + ent.name;
      if (ent.isDirectory()) walk(rel); else files.push(rel);
    }
  }
  walk('static');
  for (const f of ['go.mod', 'go.sum']) if (fs.existsSync(path.join(root, f))) files.push(f);
  const entries = files.sort().map(file => ({ path: file, sha256: sha(fs.readFileSync(path.join(root, file))) }));
  return { algorithm: 'SHA256 of UTF-8 JSON.stringify(files), sorted paths; individual file hashes over raw bytes', sha256: sha(JSON.stringify(entries)), files: entries };
}
async function listen(server) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return server.address().port;
}
async function close(server) {
  if (server.listening) await new Promise(resolve => server.close(resolve));
}
async function main() {
  fs.mkdirSync(scratch, { recursive: true });
  const before = sourceFingerprint();
  const buildArgs = ['build', '-ldflags', '-X main.version=' + version, '-o', binary, '.'];
  execFileSync(go, buildArgs, { cwd: root, stdio: 'inherit' });
  assert.equal(sourceFingerprint().sha256, before.sha256, 'Product source changed during build');
  const versionOutput = execFileSync(binary, ['--version'], { encoding: 'utf8' }).trim();
  assert.equal(versionOutput, 'vrf-ping ' + version);
  const work = fs.mkdtempSync(path.join(scratch, 'vrf-ping-readme-'));
  const listener = net.createServer(socket => {
    // Probe clients close immediately after connect; resets are normal here.
    socket.on('error', () => {});
    socket.end();
  });
  const reservation = net.createServer();
  let backend, browser, exitPromise;
  const errors = [], blocked = new Set();
  try {
    const probePort = await listen(listener);
    const webPort = await listen(reservation);
    await close(reservation);
    fs.mkdirSync(path.join(work, 'targets'));
    fs.writeFileSync(path.join(work, 'targets/ping.list'), '');
    const name = 'Loopback lab TCP';
    const targetLine = `127.0.0.1:${probePort} ${name} interval=1 source=127.0.0.1`;
    fs.writeFileSync(path.join(work, 'targets/tcp.list'), targetLine + '\n');
    const started = Date.now();
    backend = spawn(binary, ['--listen', `127.0.0.1:${webPort}`], { cwd: work, stdio: ['ignore', 'ignore', 'ignore'] });
    exitPromise = once(backend, 'exit');
    const origin = `http://127.0.0.1:${webPort}`;
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try { const r = await fetch(origin + '/api/version'); ready = r.ok; } catch {}
      if (ready) break;
      await sleep(100);
    }
    assert(ready, 'Native backend did not start');
    browser = await playwright.chromium.launch({ channel: 'msedge', headless: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1, timezoneId: 'UTC', locale: 'en-US', serviceWorkers: 'block' });
    await context.route('**/*', route => {
      if (new URL(route.request().url()).origin === origin) return route.continue();
      blocked.add(new URL(route.request().url()).origin);
      return route.abort('blockedbyclient');
    });
    const page = await context.newPage();
    page.on('pageerror', err => errors.push(err.message));
    await page.goto(origin, { waitUntil: 'networkidle' });
    await page.locator('#ver').filter({ hasText: version }).waitFor();
    // Wait real wall time: never alter the clock, history, API, or displayed DOM.
    await sleep(Math.max(0, started + 45000 - Date.now()));
    const from = new Date(Math.floor(started / 60000) * 60000).toISOString().slice(0, 16);
    const to = new Date(Math.ceil(Date.now() / 60000) * 60000).toISOString().slice(0, 16);
    await page.locator('#customFrom').fill(from);
    await page.locator('#customTo').fill(to);
    const responsePromise = page.waitForResponse(r => r.url().includes('/api/series?') && r.url().includes('&from=') && r.status() === 200);
    await page.locator('#customGo').click();
    const rounds = await (await responsePromise).json();
    assert(rounds.length >= 20, 'Expected at least 20 measured rounds');
    assert(rounds.every(r => r.t >= Math.floor(started / 1000) && r.t <= Math.ceil(Date.now() / 1000)), 'History outside this real run');
    assert(rounds.some(r => r.ms && r.ms.length), 'No real latency samples');
    await page.locator('#routingEditor summary').click();
    assert.equal(await page.locator('#routingSource').inputValue(), '127.0.0.1');
    await page.locator('#conclusion').filter({ hasText: 'custom' }).waitFor();
    await page.mouse.move(5, 5);
    await page.waitForTimeout(500);
    assert.deepEqual(errors, [], 'Browser JavaScript errors');
    assert.equal(sourceFingerprint().sha256, before.sha256, 'Product source changed during capture');
    await page.screenshot({ path: image, fullPage: false });
    const png = fs.readFileSync(image);
    const capturedAt = new Date().toISOString();
    const evidence = {
      schema: 1, caption: 'Loopback lab: genuine TCP measurements from a native vrf-ping 3.3.1-preview build; not a production network or Linux VRF demonstration.',
      capturedAt, version, versionOutput,
      source: { revision: null, reason: 'Fresh repository without a Git revision; product-content fingerprint used instead.', ...before },
      build: { command: '../toolchains/go/bin/go.exe build -ldflags "-X main.version=3.3.1-preview" -o <scratch>/vrf-ping-native-screenshot.exe .', args: buildArgs.map(a => a === binary ? '<scratch>/vrf-ping-native-screenshot' + path.extname(binary) : a), goVersion: execFileSync(go, ['version'], { encoding: 'utf8' }).trim(), binarySha256: sha(fs.readFileSync(binary)), platform: process.platform, architecture: process.arch },
      image: { path: 'docs/images/vrf-ping-graph.png', sha256: sha(png), width: png.readUInt32BE(16), height: png.readUInt32BE(20), fullPage: false, deviceScaleFactor: 1 },
      fixture: { startedAt: new Date(started).toISOString(), elapsedSeconds: (Date.now() - started) / 1000, icmpTargets: [], tcpTargets: [targetLine], listener: 'Node TCP listener owned by this capture; accepts and closes real loopback connections', sourceAddress: '127.0.0.1', vrf: null, intervalSeconds: 1, history: 'Fresh scratch directory; only executable-generated measurements; no replay, seeding, or timestamp modification', authentication: 'No credentials; HTTP bound exclusively to IPv4 loopback', externalProbes: false },
      measurements: { rounds: rounds.length, sent: rounds.reduce((n, r) => n + (r.s || 0), 0), received: rounds.reduce((n, r) => n + (r.r || 0), 0), latencySamples: rounds.reduce((n, r) => n + (r.ms || []).length, 0), firstTimestamp: rounds[0].t, lastTimestamp: rounds[rounds.length - 1].t, seriesSha256: sha(JSON.stringify(rounds)) },
      browser: { channel: 'msedge', version: browser.version(), timezone: 'UTC', thirdPartyRequestsBlocked: true, blockedOrigins: [...blocked].sort(), javascriptErrors: errors, interaction: ['Filled native custom date inputs and clicked Go', 'Expanded native Probe routing disclosure'], domRetouching: false, mockedResponses: false }
    };
    fs.writeFileSync(manifestFile, JSON.stringify(evidence, null, 2) + '\n');
    console.log(JSON.stringify({ image, manifest: manifestFile, version: versionOutput, measurements: evidence.measurements, elapsedSeconds: evidence.fixture.elapsedSeconds, sourceSha256: before.sha256, binarySha256: evidence.build.binarySha256, imageSha256: evidence.image.sha256, dimensions: [evidence.image.width, evidence.image.height] }, null, 2));
  } finally {
    if (browser) await browser.close();
    if (backend && backend.exitCode === null) { backend.kill(); await exitPromise; }
    await close(listener);
    await close(reservation);
    fs.rmSync(work, { recursive: true, force: true });
    console.log('Cleanup complete: owned browser, backend and TCP listener stopped; disposable history removed.');
  }
}
main().catch(err => { console.error(err); process.exitCode = 1; });
