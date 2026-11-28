const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadRigStudio() {
  const context = vm.createContext({
    window: { WsCompanion: {} },
    URLSearchParams,
    console
  });
  for (const file of [
    "dashboard-companion-vrm-pose.js",
    "dashboard-companion-vrm-ik.js",
    "dashboard-companion-vrm-clip.js",
    "dashboard-companion-rig-studio.js"
  ]) {
    vm.runInContext(
      fs.readFileSync(path.join(__dirname, "assets", file), "utf8"),
      context,
      { filename: file }
    );
  }
  return context.window.WsCompanion.rigStudio;
}

test("rigStudio.formatActionsPreview matches Go formatActionsEnvelopeBody rules and caps", () => {
  const rs = loadRigStudio();

  // Empty or all-private -> (none)
  assert.equal(rs.formatActionsPreview([]).text, "(none)");
  assert.equal(rs.formatActionsPreview([
    { key: "secret_move", expose_to_agent: false, description: "private" }
  ]).text, "(none)");

  // Sanitization: collapse whitespace, [ ] -> ( ), <<< / >>> neutralized, 120-rune trim, tags appended
  const longDesc = "a".repeat(130);
  const res = rs.formatActionsPreview([
    {
      key: "phone_peek",
      expose_to_agent: true,
      description: "  Glance   at [phone] <<<IGNORE>>>  ",
      expression: "happy",
      prop: "smartphone",
      loop: true
    },
    {
      key: "long_act",
      expose_to_agent: true,
      description: longDesc
    }
  ]);
  assert.equal(res.exposedCount, 2);
  assert.equal(res.shownCount, 2);
  assert.equal(res.omittedCount, 0);
  assert.equal(
    res.lines[0].text,
    "- phone_peek: Glance at (phone) ‹‹‹IGNORE››› [expression: happy] [prop: smartphone] [loop]"
  );
  assert.equal(Array.from(res.lines[1].text).length, Array.from("- long_act: ").length + 120);
  assert.ok(res.lines[1].text.endsWith("…"));

  // 24-entry cap with (+N more not shown)
  const many = [];
  for (let i = 1; i <= 27; i++) {
    many.push({ key: `act_${i}`, expose_to_agent: true, description: `Action ${i}` });
  }
  const capped = rs.formatActionsPreview(many);
  assert.equal(capped.exposedCount, 27);
  assert.equal(capped.shownCount, 24);
  assert.equal(capped.omittedCount, 3);
  assert.equal(capped.lines[capped.lines.length - 1].text, "(+3 more not shown)");

  // 3200-rune budget drops whole lines cleanly
  const bulky = [];
  for (let i = 1; i <= 30; i++) {
    bulky.push({
      key: `bulky_action_${i}`,
      expose_to_agent: true,
      description: "가".repeat(120),
      expression: "happy",
      prop: "smartphone",
      loop: true
    });
  }
  const budgeted = rs.formatActionsPreview(bulky);
  assert.ok(budgeted.runeCount <= 3200, `runeCount ${budgeted.runeCount} exceeded 3200`);
  assert.ok(budgeted.omittedCount > 0);
  assert.match(budgeted.text, /\(\+\d+ more not shown\)$/);
});

test("rigStudio.addKeyframeSnapshot enforces 8-keyframe cap and strictly ascending timestamps", () => {
  const rs = loadRigStudio();
  const clip = rs.makeDefaultClip("wave_hi", { duration_s: 2.0 });
  assert.equal(clip.keyframes.length, 2);

  for (let i = 2; i < 8; i++) {
    const r = rs.addKeyframeSnapshot(clip, 0.5); // colliding timestamp should nudge
    assert.equal(r.ok, true, `add kf #${i + 1}`);
  }
  assert.equal(clip.keyframes.length, 8);
  for (let i = 1; i < clip.keyframes.length; i++) {
    assert.ok(clip.keyframes[i].t > clip.keyframes[i - 1].t, `strictly ascending t at ${i}`);
  }

  // 9th keyframe rejected
  const over = rs.addKeyframeSnapshot(clip, 1.5);
  assert.equal(over.ok, false);
  assert.equal(over.reason, "max_keyframes");
});

test("rigStudio.buildPutPayload preserves unknown fields and Stage 2 socket_handoff markers", () => {
  const rs = loadRigStudio();
  const item = {
    key: "coffee_sip",
    isSentinel: false,
    kind: "clip",
    description: "Sip coffee thoughtfully",
    expression: "relaxed",
    prop: "smartphone",
    expose_to_agent: true,
    loop: false,
    duration_s: 1.8,
    rawEntry: {
      key: "coffee_sip",
      kind: "clip",
      file: "clips/coffee_sip.clip.json",
      future_stage2_tag: "kept-intact",
      nested_ext: { version: 2 }
    },
    clip: {
      schema: "ws-companion-clip/v1",
      key: "coffee_sip",
      duration_s: 1.8,
      loop: false,
      easing: "ease_out_back",
      future_clip_meta: { author: "studio" },
      keyframes: [
        {
          t: 0.0,
          label: "grab",
          socket_handoff: { prop: "smartphone", to_socket: "hand_r" },
          custom_kf_ext: 42
        }
      ]
    }
  };

  const payload = JSON.parse(JSON.stringify(rs.buildPutPayload(item)));
  assert.equal(payload.entry.future_stage2_tag, "kept-intact");
  assert.deepEqual(payload.entry.nested_ext, { version: 2 });
  assert.equal(payload.entry.expose_to_agent, true);
  assert.equal(payload.clip.future_clip_meta.author, "studio");
  assert.deepEqual(payload.clip.keyframes[0].socket_handoff, { prop: "smartphone", to_socket: "hand_r" });
  assert.equal(payload.clip.keyframes[0].custom_kf_ext, 42);
});

test("rigStudio validates keys, descriptions, and pack authorability rules", () => {
  const rs = loadRigStudio();
  assert.equal(rs.validateActionKey("wave_hi_2").ok, true);
  assert.equal(rs.validateActionKey("Idle").ok, false);
  assert.equal(rs.validateActionKey("../evil").ok, false);
  assert.equal(rs.validateActionKey("con").ok, false);
  assert.equal(rs.validateActionKey("lpt1").ok, false);

  assert.equal(rs.validateDescription("Wave warmly at the user").ok, true);
  assert.equal(rs.validateDescription("a".repeat(121)).ok, false);
  assert.equal(rs.validateDescription("bad\u202Ebidi").ok, false);
  assert.equal(rs.validateDescription("bad\nnewline").ok, false);

  assert.equal(rs.isPackAuthorable({ id: "default", renderer: "2d", builtin: true }).ok, false);
  assert.equal(rs.isPackAuthorable({ id: "my2d", renderer: "2d" }).ok, false);
  assert.equal(rs.isPackAuthorable({ id: "rem", renderer: "vrm", origin: { kind: "git" } }).ok, false);
  assert.equal(rs.isPackAuthorable({ id: "local_vrm", renderer: "vrm", origin: { kind: "local" } }).ok, true);
  assert.equal(rs.isPackAuthorable({ id: "handmade_vrm", renderer: "vrm" }).ok, true);
});

test("Settings companion Open Studio button enables only for authorable local VRM packs", () => {
  const src = fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-companion.js"), "utf8");
  assert.match(src, /function studioEligibility\(pack\)/);
  assert.match(src, /\/companion\/studio\?pack=/);
  assert.match(src, /dataset\.companionOpenStudio/);
  assert.match(src, /open_studio_disabled_builtin/);
  assert.match(src, /open_studio_disabled_2d/);
  assert.match(src, /open_studio_disabled_remote/);
});
