'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { createRequire } = require('node:module');
const { ConfigStore } = require('../src/config-store.cjs');

function fixture(t, { lock = true, legacyStatus = 0, legacyError } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfassistant-migration-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const destination = path.join(root, 'KSFAssistant', 'settings.json');
  const source = path.join(root, 'CodexAssistant', 'settings.json');
  const fallback = path.join(root, 'Codex Usage Bar', 'settings.json');
  const write = (filePath, value) => {
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, typeof value === 'string' ? value : JSON.stringify(value));
  };
  const events = [];
  let ready;
  const app = {
    setAppUserModelId: (value) => events.push(['identity', value]),
    setName: (value) => events.push(['name', value]),
    setPath: (name, value) => events.push(['path', name, value]),
    requestSingleInstanceLock: () => { events.push(['lock']); return lock; },
    getPath: (name) => name === 'appData' ? root : path.dirname(destination),
    whenReady: () => ({ then: (callback) => { ready = callback; } }),
    on: () => {},
    exit: (code) => events.push(['exit', code]),
  };
  const mainPath = path.resolve(__dirname, '../src/main.cjs');
  const localRequire = createRequire(mainPath);
  const context = vm.createContext({
    require: (name) => {
      if (name === 'electron') return {
        app,
        dialog: { showErrorBox: (...args) => events.push(['error', ...args]) },
      };
      if (name === './core-client.cjs') return {
        CoreClient: class {
          constructor(options) {
            events.push(['core', options]);
            throw new Error('test boundary: Core construction');
          }
        },
      };
      if (name === 'node:child_process') return {
        spawn: () => { throw new Error('unexpected process launch'); },
        spawnSync: (command, args, options) => {
          if (command === 'powershell.exe' && args.join(' ').includes('Get-Process')) {
            events.push(['legacy-check', command, args, options]);
            return { status: legacyStatus, error: legacyError };
          }
          if (command === 'schtasks.exe') {
            events.push(['scheduled-task', args]);
            return { status: 0 };
          }
          throw new Error('unexpected process launch');
        },
      };
      return localRequire(name);
    },
    __dirname: path.dirname(mainPath),
    process: { ...process, platform: 'win32', env: {} },
    console,
  });
  vm.runInContext(fs.readFileSync(mainPath, 'utf8'), context, { filename: mainPath });
  const migrate = vm.runInContext('migrateLegacySettings', context);
  return { root, destination, source, fallback, write, events, migrate: () => migrate(destination), start: () => ready() };
}

test('new userData and single-instance lock precede startup migration', async (t) => {
  const setup = fixture(t, { lock: false });
  setup.write(setup.source, { ksfRoot: 'legacy' });
  await setup.start();
  const pathIndex = setup.events.findIndex((event) => event[0] === 'path' && event[1] === 'userData' && event[2] === path.dirname(setup.destination));
  assert.ok(pathIndex >= 0);
  assert.ok(pathIndex < setup.events.findIndex((event) => event[0] === 'lock'));
  assert.ok(setup.events.some((event) => event[0] === 'lock'));
  assert.ok(setup.events.some((event) => event[0] === 'exit'));
  assert.equal(setup.events.some((event) => event[0] === 'legacy-check'), false);
  assert.equal(fs.existsSync(setup.destination), false);
  assert.equal(setup.events.some((event) => event[0] === 'scheduled-task' || event[0] === 'core'), false);
});

test('healthy repeated startups migrate before Core and retain the shared Feishu data path', async (t) => {
  const setup = fixture(t);
  setup.write(setup.source, { ksfRoot: 'legacy', future: { retained: true } });
  const sharedData = path.join(setup.root, '.config', 'feishu-bridge', 'client.json');
  const credentials = path.join(setup.root, '.config', 'feishu-bridge', 'credentials.enc');
  setup.write(sharedData, { sentinel: 'shared data must stay in place' });
  setup.write(credentials, 'synthetic encrypted fixture');
  await assert.rejects(setup.start(), /test boundary: Core construction/);
  assert.equal(new ConfigStore(setup.destination).get().ksfRoot, 'legacy');
  assert.ok(setup.events.findIndex((event) => event[0] === 'lock') < setup.events.findIndex((event) => event[0] === 'legacy-check'));
  assert.ok(setup.events.findIndex((event) => event[0] === 'legacy-check') < setup.events.findIndex((event) => event[0] === 'core'));
  const core = setup.events.find((event) => event[0] === 'core')[1];
  assert.equal(core.integrations.ksfRoot, 'legacy');
  assert.equal(core.env.FEISHU_BRIDGE_DATA_DIR, path.join(os.homedir(), '.config', 'feishu-bridge'));
  new ConfigStore(setup.destination).update({ ksfRoot: 'updated' });
  const updated = fs.readFileSync(setup.destination, 'utf8');
  await assert.rejects(setup.start(), /test boundary: Core construction/);
  assert.equal(fs.readFileSync(setup.destination, 'utf8'), updated);
  assert.equal(fs.readFileSync(credentials, 'utf8'), 'synthetic encrypted fixture');
  assert.deepEqual(JSON.parse(fs.readFileSync(sharedData, 'utf8')), { sentinel: 'shared data must stay in place' });
});

for (const options of [{ legacyStatus: 10 }, { legacyStatus: 1 }, { legacyStatus: null, legacyError: new Error('powershell unavailable') }]) {
  test(`startup refuses old-app coexistence or an unverifiable process check: ${options.legacyStatus}`, async (t) => {
    const setup = fixture(t, options);
    setup.write(setup.source, { ksfRoot: 'legacy' });
    await setup.start();
    assert.ok(setup.events.some((event) => event[0] === 'legacy-check'));
    assert.ok(setup.events.some((event) => event[0] === 'error'));
    assert.deepEqual(setup.events.at(-1), ['exit', 1]);
    assert.equal(fs.existsSync(setup.destination), false);
    assert.equal(setup.events.some((event) => event[0] === 'scheduled-task' || event[0] === 'core'), false);
    const check = setup.events.find((event) => event[0] === 'legacy-check');
    assert.match(check[2].join(' '), /\$_.ProcessName -in @\('CodexAssistant', 'Codex Usage Bar', 'CodexUsageBar'\)/);
    assert.doesNotMatch(check[2].join(' '), /Stop-Process|taskkill|CommandLine/i);
    assert.equal(check[3].windowsHide, true);
    assert.ok(check[3].timeout > 0);
  });
}

test('CodexAssistant takes precedence and only exact catalog project pins migrate', (t) => {
  const setup = fixture(t);
  const legacy = {
    ksfRoot: 'C:\\preferred',
    future: { retained: ['CodexAssistant', 'Codex Usage Bar'] },
    feishuBridgeRoot: 'C:\\shared-bridge',
    pinnedProjectIds: ['10项目/Codex Usage Bar/项目记忆卡.md', '10项目/KSFAssistant/项目记忆卡.md', 'project:Codex Usage Bar', '10项目/Codex Usage Bar Extra/项目记忆卡.md'],
  };
  setup.write(setup.source, legacy);
  setup.write(setup.fallback, { ksfRoot: 'C:\\fallback' });
  const original = fs.readFileSync(setup.source, 'utf8');
  setup.migrate();
  const disk = JSON.parse(fs.readFileSync(setup.destination, 'utf8'));
  assert.equal(disk.ksfRoot, legacy.ksfRoot);
  assert.deepEqual(disk.future, legacy.future);
  assert.equal(disk.feishuBridgeRoot, legacy.feishuBridgeRoot);
  assert.deepEqual(new ConfigStore(setup.destination).get().pinnedProjectIds, ['10项目/KSFAssistant/项目记忆卡.md', 'project:Codex Usage Bar', '10项目/Codex Usage Bar Extra/项目记忆卡.md']);
  assert.equal(fs.readFileSync(setup.source, 'utf8'), original);
});

test('Codex Usage Bar is the fallback only when CodexAssistant is absent', (t) => {
  const setup = fixture(t);
  setup.write(setup.fallback, { ksfRoot: 'fallback', future: { preserved: true } });
  setup.migrate();
  assert.equal(new ConfigStore(setup.destination).get().ksfRoot, 'fallback');
  assert.deepEqual(JSON.parse(fs.readFileSync(setup.destination, 'utf8')).future, { preserved: true });
});

test('no legacy settings is a clean first launch without creating a destination', (t) => {
  const setup = fixture(t);
  setup.migrate();
  assert.equal(fs.existsSync(setup.destination), false);
  assert.equal(new ConfigStore(setup.destination).get().ksfRoot, '');
});

test('existing destination wins byte-for-byte without reading corrupt legacy settings', (t) => {
  const setup = fixture(t);
  const existing = '{ "ksfRoot": "current", "future": { "preserved": true } }\n';
  setup.write(setup.destination, existing);
  setup.write(setup.source, '{corrupt');
  setup.migrate();
  assert.equal(fs.readFileSync(setup.destination, 'utf8'), existing);
});

test('skipped migration remaps existing pins only in DTO until the next explicit update', (t) => {
  const setup = fixture(t);
  const existing = '{ "pinnedProjectIds": ["10项目/Codex Usage Bar/项目记忆卡.md", "project:Codex Usage Bar"], "future": { "retained": true } }\n';
  setup.write(setup.destination, existing);
  setup.write(setup.source, '{corrupt legacy must not be read');
  setup.migrate();
  const store = new ConfigStore(setup.destination);
  const expectedPins = ['10项目/KSFAssistant/项目记忆卡.md', 'project:Codex Usage Bar'];
  assert.deepEqual(store.get().pinnedProjectIds, expectedPins);
  assert.equal(store.get().future, undefined);
  assert.equal(fs.readFileSync(setup.destination, 'utf8'), existing);
  store.update({ launchAtLogin: true });
  const persisted = JSON.parse(fs.readFileSync(setup.destination, 'utf8'));
  assert.deepEqual(persisted.pinnedProjectIds, expectedPins);
  assert.deepEqual(persisted.future, { retained: true });
});

test('repeated startups do not recopy or overwrite updated destination settings', (t) => {
  const setup = fixture(t);
  setup.write(setup.source, { ksfRoot: 'legacy', future: { retained: true } });
  setup.migrate();
  const store = new ConfigStore(setup.destination);
  store.update({ ksfRoot: 'updated' });
  const updated = fs.readFileSync(setup.destination, 'utf8');
  setup.write(setup.source, '{now-corrupt');
  setup.migrate();
  setup.migrate();
  assert.equal(fs.readFileSync(setup.destination, 'utf8'), updated);
  assert.equal(new ConfigStore(setup.destination).get().ksfRoot, 'updated');
  assert.deepEqual(JSON.parse(updated).future, { retained: true });
});

for (const invalid of ['{broken', 'null', '[]', '42']) {
  test(`corrupt preferred source never falls back or creates a destination: ${invalid}`, (t) => {
    const setup = fixture(t);
    setup.write(setup.source, invalid);
    setup.write(setup.fallback, { ksfRoot: 'fallback' });
    assert.throws(setup.migrate, /settings|configuration|配置/i);
    assert.equal(fs.existsSync(setup.destination), false);
    assert.equal(fs.readFileSync(setup.source, 'utf8'), invalid);
  });
}

test('legacy read failure aborts instead of trying the fallback', (t) => {
  const setup = fixture(t);
  setup.write(setup.source, {});
  setup.write(setup.fallback, { ksfRoot: 'fallback' });
  const read = fs.readFileSync;
  t.mock.method(fs, 'readFileSync', (target, ...args) => {
    if (target === setup.source) throw Object.assign(new Error('read denied'), { code: 'EACCES' });
    return read(target, ...args);
  });
  assert.throws(setup.migrate, /settings|configuration|配置/i);
  assert.equal(fs.existsSync(setup.destination), false);
});

for (const failure of ['writeFileSync', 'linkSync']) {
  test(`migration ${failure} failure leaves no destination, preserves source and permits retry`, (t) => {
    const setup = fixture(t);
    setup.write(setup.source, { ksfRoot: 'legacy' });
    const original = fs.readFileSync(setup.source, 'utf8');
    const mocked = t.mock.method(fs, failure, () => { throw Object.assign(new Error('injected migration failure'), { code: 'EIO' }); });
    assert.throws(setup.migrate, /migration|settings|配置|迁移/i);
    mocked.mock.restore();
    assert.equal(fs.existsSync(setup.destination), false);
    assert.equal(fs.readFileSync(setup.source, 'utf8'), original);
    assert.deepEqual(fs.readdirSync(path.dirname(setup.destination)), []);
    setup.migrate();
    assert.equal(new ConfigStore(setup.destination).get().ksfRoot, 'legacy');
  });
}

test('partial temporary write failure aborts startup with no published settings', async (t) => {
  const setup = fixture(t);
  setup.write(setup.source, { ksfRoot: 'legacy' });
  const write = fs.writeFileSync;
  t.mock.method(fs, 'writeFileSync', (filePath) => {
    write(filePath, '{partial');
    throw Object.assign(new Error('disk full'), { code: 'ENOSPC' });
  });
  await setup.start();
  assert.deepEqual(setup.events.at(-1), ['exit', 1]);
  assert.ok(setup.events.some((event) => event[0] === 'error' && /ENOSPC/.test(event[2])));
  assert.equal(setup.events.some((event) => event[0] === 'scheduled-task' || event[0] === 'core'), false);
  assert.equal(fs.existsSync(setup.destination), false);
  assert.deepEqual(fs.readdirSync(path.dirname(setup.destination)), []);
  assert.deepEqual(JSON.parse(fs.readFileSync(setup.source, 'utf8')), { ksfRoot: 'legacy' });
});

test('a corrupt fallback also aborts instead of becoming the new settings', (t) => {
  const setup = fixture(t);
  setup.write(setup.fallback, '{corrupt');
  assert.throws(setup.migrate, /设置/);
  assert.equal(fs.existsSync(setup.destination), false);
});

test('destination metadata errors abort without reading legacy settings', (t) => {
  const setup = fixture(t);
  setup.write(setup.source, { ksfRoot: 'legacy' });
  const lstat = fs.lstatSync;
  t.mock.method(fs, 'lstatSync', (filePath, ...args) => {
    if (filePath === setup.destination) throw Object.assign(new Error('metadata denied'), { code: 'EACCES' });
    return lstat(filePath, ...args);
  });
  assert.throws(setup.migrate, /EACCES/);
  assert.equal(fs.existsSync(setup.destination), false);
});

test('atomic publication cannot overwrite a destination created during migration', (t) => {
  const setup = fixture(t);
  setup.write(setup.source, { ksfRoot: 'legacy' });
  const link = fs.linkSync;
  const winner = '{ "ksfRoot": "concurrent winner", "future": true }';
  t.mock.method(fs, 'linkSync', (temporary, destination) => {
    assert.equal(fs.existsSync(destination), false);
    assert.equal(JSON.parse(fs.readFileSync(temporary, 'utf8')).ksfRoot, 'legacy');
    fs.writeFileSync(destination, winner, { flag: 'wx' });
    return link(temporary, destination);
  });
  setup.migrate();
  assert.equal(fs.readFileSync(setup.destination, 'utf8'), winner);
  assert.deepEqual(fs.readdirSync(path.dirname(setup.destination)), ['settings.json']);
});

for (const location of ['source', 'destination']) {
  test(`startup explicitly reports corrupt ${location} and exits before any process launches`, async (t) => {
    const setup = fixture(t);
    setup.write(setup[location], '{broken');
    await setup.start();
    assert.ok(setup.events.some((event) => event[0] === 'error' && /设置|settings/i.test(event[1])));
    assert.deepEqual(setup.events.at(-1), ['exit', 1]);
    assert.ok(setup.events.some((event) => event[0] === 'identity' && event[1] === 'com.ksfassistant.desktop'));
    assert.ok(setup.events.some((event) => event[0] === 'name' && event[1] === 'KSFAssistant'));
    assert.equal(setup.events.some((event) => event[0] === 'scheduled-task' || event[0] === 'core'), false);
  });
}
