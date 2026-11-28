const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const assets = path.join(__dirname, "assets");
const read = name => fs.readFileSync(path.join(assets, name), "utf8");

test("WsInput queues voice fields until the voice bundle flushes them", () => {
  const source = read("ws-input.js");
  assert.match(source, /var voiceQueue = \[\]/);
  assert.match(source, /attachVoice\(api\)/);
  assert.match(source, /flushVoice: flushVoice/);
  assert.match(source, /if \(api\.voice\) \{ api\.voice\.destroy\(\); api\.voice = null; \}/);
});

test("secondary PTT fields share one document controller and capture Escape", () => {
  const source = read("dashboard-voice-field.js");
  assert.match(source, /doc\.__wsVoiceFieldHub/);
  assert.match(source, /voice\.createController/);
  assert.match(source, /"keydown", onEscape, true/);
  assert.match(source, /e\.preventDefault\(\); e\.stopPropagation\(\)/);
  assert.match(source, /handleDocumentKeyUp/);
  assert.match(source, /data-floating/);
  assert.match(source, /field\.cap\.parentNode\.removeChild/);
});

test("all six shared WsInput surfaces opt into voice", () => {
  [
    "dashboard-composer-pickers.js", "dashboard-composer-selections.js", "dashboard-composer.js",
    "dashboard-companion-messenger.js", "memo.js", "dashboard-tracker.js"
  ].forEach(name => assert.match(read(name), /voice: true/));
});

test("bundle ordering and Escape isolation preserve the primary composer", () => {
  const pages = fs.readFileSync(path.join(__dirname, "pages.go"), "utf8");
  assert.ok(pages.indexOf('"dashboard-voice.js"') < pages.indexOf('"dashboard-voice-field.js"'));
  assert.match(read("dashboard-companion-messenger.js"), /if \(event\.defaultPrevented\) return;/);
  assert.match(read("dashboard-voice-ptt.js"), /globalEvents/);
  assert.match(read("dashboard-voice.css"), /\[data-voice-locked="1"\]/);
});
