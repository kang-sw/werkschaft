'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {
  build,
  checkBinary,
  resourceMap,
  bundleResourceMap,
  comparePackage,
  releaseConfig,
  readMacPlist,
  verifyMacVoicePlist,
  verifyMacVoiceEntitlements,
} = require('./package.cjs');

test('maps dotfiles and nested files to their exact daemon resource paths', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'werkschaft-map-test-'));
  const other = fs.mkdtempSync(path.join(os.tmpdir(), 'werkschaft-map-test-'));
  try {
    fs.mkdirSync(path.join(root, '.claude-plugin'));
    fs.writeFileSync(path.join(root, '.claude-plugin', 'plugin.json'), '{}');
    fs.writeFileSync(path.join(root, 'ws-mcp'), 'binary');
    const map = resourceMap(root);
    assert.equal(map[path.join(root, '.claude-plugin', 'plugin.json')], 'daemon/.claude-plugin/plugin.json');
    assert.equal(map[path.join(root, 'ws-mcp')], 'daemon/ws-mcp');
    assert.throws(() => comparePackage(root, other, 'darwin'), /file list differs/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); fs.rmSync(other, { recursive: true, force: true }); }
});

test('resolves Windows resource sources on the checkout drive and preserves bundle destinations', () => {
  const windows = path.win32;
  const base = 'D:\\a\\werkschaft\\werkschaft\\agents-plugin-tool\\desktop';
  const stage = windows.join(base, 'target', 'p-test case');
  const manual = windows.join(stage, 'daemon', 'rsrc', 'lead-workflow-manual', 'command-syntax.md');
  const binary = windows.join(stage, 'daemon', 'ws-mcp.exe');
  const resources = bundleResourceMap({
    [manual]: 'daemon/rsrc/lead-workflow-manual/command-syntax.md',
    [binary]: 'daemon/ws-mcp.exe',
  }, base, windows);
  assert.deepEqual(resources, {
    'target/p-test case/daemon/rsrc/lead-workflow-manual/command-syntax.md': 'daemon/rsrc/lead-workflow-manual/command-syntax.md',
    'target/p-test case/daemon/ws-mcp.exe': 'daemon/ws-mcp.exe',
  });
  assert.deepEqual(Object.keys(resources).map(source => windows.resolve(base, source)), [manual, binary]);
  assert.deepEqual(Object.values(resources).map(destination => windows.resolve('C:\\installed', destination)), [
    'C:\\installed\\daemon\\rsrc\\lead-workflow-manual\\command-syntax.md',
    'C:\\installed\\daemon\\ws-mcp.exe',
  ]);
  assert.throws(() => bundleResourceMap({ 'C:\\Temp\\ws-mcp.exe': 'daemon/ws-mcp.exe' }, base, windows),
    /different drive/);
});

test('rejects missing input and wrong architecture before invoking Tauri', () => {
  assert.throws(() => build({ input: 'relative', platform: 'darwin' }), /absolute assembled package/);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'werkschaft-binary-test-'));
  try {
    const file = path.join(root, 'binary');
    fs.writeFileSync(file, 'not a binary');
    assert.throws(() => checkBinary(file, 'darwin'), /darwin-arm64/);
    assert.throws(() => checkBinary(file, 'win32'), /win32-x64/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test('release overlays retain the exact updater version and platform installer contracts', () => {
  const version = '0.50.8-canary.2';
  const pubkey = 'encoded-public-key';
  const resources = { 'target/p-stage/daemon/ws-mcp': 'daemon/ws-mcp' };
  const mac = releaseConfig({ version, pubkey, resources, platform: 'darwin' });
  const windows = releaseConfig({ version, pubkey, resources, platform: 'win32' });
  for (const config of [mac, windows]) {
    assert.equal(config.version, version);
    assert.deepEqual(config.plugins.updater, { pubkey, requireSignedVersion: true, windows: { installMode: 'passive' } });
    assert.equal(config.bundle.createUpdaterArtifacts, true);
    assert.deepEqual(config.bundle.resources, resources);
  }
  assert.equal(mac.mainBinaryName, 'Werkschaft');
  assert.deepEqual(mac.bundle.targets, ['app']);
  assert.equal(windows.mainBinaryName, undefined);
  assert.deepEqual(windows.bundle.targets, ['nsis', 'msi']);
  assert.deepEqual(windows.bundle.windows, {
    webviewInstallMode: { type: 'embedBootstrapper', silent: true },
    nsis: { installMode: 'currentUser', installerHooks: path.resolve(__dirname, '..', 'installer-hooks.nsh') },
    allowDowngrades: true, wix: { version: '0.50.8' },
  });
  // An upgrade must not keep a daemon/ file the new release dropped.
  const hooks = fs.readFileSync(windows.bundle.windows.nsis.installerHooks, 'utf8');
  assert.match(hooks, /!macro NSIS_HOOK_PREINSTALL\s+RMDir \/r "\$INSTDIR\\daemon"\s+!macroend/);
});

test('macOS voice bundle verification requires microphone and speech usage descriptions and audio-input entitlement', () => {
  verifyMacVoicePlist({
    NSMicrophoneUsageDescription: 'Push-to-talk voice typing',
    NSSpeechRecognitionUsageDescription: 'Native voice typing',
  });
  assert.throws(() => verifyMacVoicePlist({}), /NSMicrophoneUsageDescription/);
  assert.throws(() => verifyMacVoicePlist({ NSMicrophoneUsageDescription: '   ', NSSpeechRecognitionUsageDescription: 'ok' }), /NSMicrophoneUsageDescription/);
  assert.throws(() => verifyMacVoicePlist({ NSMicrophoneUsageDescription: 'ok' }), /NSSpeechRecognitionUsageDescription/);
  assert.throws(() => verifyMacVoicePlist({ NSMicrophoneUsageDescription: 'ok', NSSpeechRecognitionUsageDescription: '   ' }), /NSSpeechRecognitionUsageDescription/);

  verifyMacVoiceEntitlements({ 'com.apple.security.device.audio-input': true });
  assert.throws(() => verifyMacVoiceEntitlements({}), /com\.apple\.security\.device\.audio-input/);
  assert.throws(
    () => verifyMacVoiceEntitlements({ 'com.apple.security.device.audio-input': false }),
    /com\.apple\.security\.device\.audio-input/,
  );

  const desktopDir = path.resolve(__dirname, '..');
  const tauriConf = JSON.parse(fs.readFileSync(path.join(desktopDir, 'tauri.conf.json'), 'utf8'));
  assert.equal(tauriConf.app.withGlobalTauri, false);
  assert.equal(tauriConf.app.security.capabilities[0].identifier, 'no-native-authority');
  assert.deepEqual(tauriConf.app.security.capabilities[0].permissions, []);
  assert.equal(tauriConf.bundle.macOS.infoPlist, 'Info.plist');
  assert.equal(tauriConf.bundle.macOS.entitlements, 'entitlements.plist');

  const infoPlistPath = path.join(desktopDir, tauriConf.bundle.macOS.infoPlist);
  const entitlementsPath = path.join(desktopDir, tauriConf.bundle.macOS.entitlements);
  assert.ok(fs.existsSync(infoPlistPath), 'desktop/Info.plist must exist');
  assert.ok(fs.existsSync(entitlementsPath), 'desktop/entitlements.plist must exist');
  if (process.platform === 'darwin') {
    verifyMacVoicePlist(readMacPlist(infoPlistPath));
    verifyMacVoiceEntitlements(readMacPlist(entitlementsPath));
  } else {
    const rawPlist = fs.readFileSync(infoPlistPath, 'utf8');
    assert.match(rawPlist, /<key>NSMicrophoneUsageDescription<\/key>/);
    assert.match(rawPlist, /<key>NSSpeechRecognitionUsageDescription<\/key>/);
    assert.match(fs.readFileSync(entitlementsPath, 'utf8'), /<key>com\.apple\.security\.device\.audio-input<\/key>\s*<true\/>/);
  }
});
