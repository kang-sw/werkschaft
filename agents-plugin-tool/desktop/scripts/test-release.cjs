'use strict';
const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { PLATFORMS, releaseVersion, writePlatformManifest, combine, verifyRelease } = require('./release.cjs');
const { publicKey, verifySignature } = require('./release-signature.cjs');

// Offline test vector from tauri-plugin-updater 2.14.1's updater.rs. Its
// payload is the literal string 'test'. The release tests need no private key.
const base64 = text => Buffer.from(text).toString('base64');
const key = publicKey(base64([
  'untrusted comment: minisign public key: A49EF4746FA6E6D7',
  'RWTX5qZvdPSepDGWg2bz8n6ArQnBMwgQK1geVosorcbLzcunD+KJ+cYe',
].join('\n')));
const signatureText = [
  'untrusted comment: signature from tauri secret key',
  'RUTX5qZvdPSepPF2zsjvP/L/TQtBLRnWRvaq+4omK1UEugtQp/USrGiI1v762sDy3id3aor7bRC9ETyeot6lMHHrWoZ5hjUM1Qc=',
  'trusted comment: timestamp:1791649051\tfile:payload\tversion:2.0.0',
  'DDq2CLOEl5cQmC2eJRHlnZPVViOZsu1CjpPKQoei4A/XBfkDqGO0B+QMrcrHgEQ5qV3mne75tdFWhJpqUXh9AA==',
].join('\n');
const signature = base64(signatureText);

test('verifies the Tauri signature and binds the claimed version', () => {
  const hash = payload => createHash('blake2b512').update(payload).digest();
  verifySignature(key, signature, hash('test'), '2.0.0');
  assert.throws(() => verifySignature(key, '', hash('test'), '2.0.0'), /base64/);
  assert.throws(() => verifySignature(key, signature, hash('tampered'), '2.0.0'), /verify/);
  assert.throws(() => verifySignature(key, signature, hash('test'), '2.0.1'), /does not bind/);
  assert.throws(() => verifySignature(key, base64(signatureText.replace('version:2.0.0', 'version:2.0.1')),
    hash('test'), '2.0.1'), /verify/);
});

test('requires exact stable and canary versions for release manifests', async () => {
  assert.equal(releaseVersion('0.50.8').channel, 'stable');
  assert.equal(releaseVersion('0.50.8-canary.0').channel, 'canary');
  for (const version of ['0.50.8-dev', 'v0.50.8', '0.050.8', '0.50.8-canary.01', '0.50.8+21', '0.50.8\n', '9007199254740992.0.0', '0.1.0-canary.9007199254740992']) {
    assert.throws(() => releaseVersion(version), /exact version/);
  }
  await assert.rejects(writePlatformManifest('', 'darwin', '0.50.8-dev', key), /exact version/);
});

test('combines and independently verifies the complete stable release with Tauri payloads', async t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'tauri-release-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const output = path.join(root, 'combined');
  const version = '2.0.0';
  for (const [platform, { arch, assets }] of Object.entries(PLATFORMS)) {
    const dir = path.join(root, `tauri-${platform}-${arch}`);
    fs.mkdirSync(dir);
    for (const name of Object.values(assets)) {
      fs.writeFileSync(path.join(dir, name), 'test');
      fs.writeFileSync(path.join(dir, `${name}.sig`), signature);
    }
    await writePlatformManifest(dir, platform, version, key);
  }
  await combine(root, output, version, key);
  await verifyRelease(output, version, key);
  const feed = JSON.parse(fs.readFileSync(path.join(output, 'werkschaft-tauri-update.json'), 'utf8'));
  assert.equal(feed.version, version);
  assert.deepEqual(Object.keys(feed.platforms), ['darwin-aarch64', 'windows-x86_64-nsis', 'windows-x86_64-msi']);
  assert.deepEqual(feed.platforms['windows-x86_64-nsis'], {
    url: 'https://github.com/kang-sw/werkschaft/releases/download/v2.0.0/Werkschaft-win32-x64-setup.exe',
    signature, sha256: createHash('sha256').update('test').digest('hex'), size: 4,
  });
  assert.equal(fs.readdirSync(output).length, 7);
  const feedFile = path.join(output, 'werkschaft-tauri-update.json');
  const originalFeed = fs.readFileSync(feedFile);
  fs.writeFileSync(feedFile, '{}');
  await assert.rejects(verifyRelease(output, version, key), /feed differs/);
  fs.writeFileSync(feedFile, originalFeed);
  const retired = path.join(output, 'Werkschaft-desktop-SHA256SUMS');
  fs.writeFileSync(retired, '');
  await assert.rejects(verifyRelease(output, version, key), /Unexpected release files/);
  fs.unlinkSync(retired);
  fs.appendFileSync(path.join(output, 'Werkschaft-win32-x64-setup.exe'), 'changed');
  await assert.rejects(verifyRelease(output, version, key), /verify/);
  fs.appendFileSync(path.join(root, 'tauri-win32-x64', 'Werkschaft-win32-x64-setup.exe'), 'changed');
  const invalid = path.join(root, 'invalid');
  await assert.rejects(combine(root, invalid, version, key), /verify/);
  assert.equal(fs.existsSync(invalid), false);
});

test('combines signed canary payloads with the same Tauri-only artifact contract', async t => {
  const { generateKeyPairSync, sign } = require('node:crypto');
  const { publicKey: pub, privateKey } = generateKeyPairSync('ed25519');
  const key = { id: Buffer.alloc(8, 1), key: pub };
  const version = '2.0.0-canary.1';
  const raw = sign(null, createHash('blake2b512').update('test').digest(), privateKey);
  const comment = 'timestamp:1\tfile:payload\tversion:' + version;
  const signature = base64([
    'untrusted comment: fixture',
    Buffer.concat([Buffer.from('ED'), key.id, raw]).toString('base64'),
    'trusted comment: ' + comment,
    sign(null, Buffer.concat([raw, Buffer.from(comment)]), privateKey).toString('base64'),
  ].join('\n'));
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'tauri-canary-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const [platform, { arch, assets }] of Object.entries(PLATFORMS)) {
    const dir = path.join(root, 'tauri-' + platform + '-' + arch);
    fs.mkdirSync(dir);
    for (const name of Object.values(assets)) {
      fs.writeFileSync(path.join(dir, name), 'test');
      fs.writeFileSync(path.join(dir, name + '.sig'), signature);
    }
    await writePlatformManifest(dir, platform, version, key);
  }
  const output = path.join(root, 'combined');
  await combine(root, output, version, key);
  await verifyRelease(output, version, key);
  assert.equal(fs.readdirSync(output).length, 7);
  const feed = JSON.parse(fs.readFileSync(path.join(output, 'werkschaft-tauri-update.json')));
  assert.equal(feed.version, version);
  assert.equal(Object.keys(feed.platforms).length, 3);
  for (const entry of Object.values(feed.platforms)) {
    assert(entry.url.includes('/v' + version + '/'));
    assert.equal(entry.signature, signature);
    assert.equal(entry.size, 4);
  }
  const platform = path.join(root, 'tauri-win32-x64', 'platform.json');
  fs.writeFileSync(platform, '{}');
  await assert.rejects(combine(root, path.join(root, 'invalid'), version, key), /digests differ/);
});
