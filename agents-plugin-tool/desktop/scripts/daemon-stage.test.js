'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { fixture } = require('./daemon-fixture.cjs');
const { copyPackage } = require('./daemon-stage.cjs');

for (const platform of ['darwin', 'win32']) test('copies and validates the entire prepared ' + platform + ' package', t => {
  const f = fixture(t, platform), destination = path.join(f.base, 'copy');
  const staged = copyPackage(f.bundled.root, destination, 'bundled', platform);
  assert.deepEqual(fs.readFileSync(staged.binary), fs.readFileSync(f.bundled.binary));
  assert(fs.existsSync(path.join(destination, 'bin/ws-mcp-launcher.py')));
  assert.throws(() => copyPackage(f.bundled.root, destination, 'bundled', platform), /not an empty directory/);
  const link = path.join(f.bundled.root, 'extra-link');
  fs.symlinkSync(f.bundled.binary, link);
  assert.throws(() => copyPackage(f.bundled.root, path.join(f.base, 'linked'), 'bundled', platform), /link or special/);
  assert.equal(fs.existsSync(path.join(f.base, 'linked')), false);
});
