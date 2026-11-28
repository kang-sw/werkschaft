(function () {
  // Unified, instance-scoped multi-line input controller (window.WsInput).
  //
  // Every attached surface owns isolated state: value/draft, mentions,
  // capsules, @/#// popups, live markdown rendering via window.WsMdEditor,
  // keymap, and attachments. Multiple simultaneous instances in one document
  // never share module-level editor globals.

  var doc = document;

  function wsT(key, vars) {
    return typeof window.wsT === "function" ? window.wsT(key, vars) : key;
  }

  // --- Shared editing helpers -----------------------------------------------
  function indentLines(field, out, editFn) {
    if (!field) return false;
    var v = field.value, s = field.selectionStart, e = field.selectionEnd;
    var apply = editFn || function (a, b, text) {
      field.setRangeText(text, a, b, "end");
      field.dispatchEvent(new Event("input", { bubbles: true }));
    };
    if (!out && s === e) {
      apply(s, e, "  ");
      return true;
    }
    var ls = v.lastIndexOf("\n", s - 1) + 1;
    var le = e > s && v.charAt(e - 1) === "\n" ? e - 1 : v.indexOf("\n", e);
    if (le < 0) le = v.length;
    var lines = v.slice(ls, le).split("\n"), firstDelta = 0, totalDelta = 0;
    var next = lines.map(function (line, i) {
      if (!out) {
        if (i === 0) firstDelta = 2;
        totalDelta += 2;
        return "  " + line;
      }
      var m = /^(?: {1,2}|\t)/.exec(line);
      var drop = m ? m[0].length : 0;
      if (i === 0) firstDelta = -drop;
      totalDelta -= drop;
      return line.slice(drop);
    }).join("\n");
    if (next === v.slice(ls, le)) return true;
    apply(ls, le, next);
    var ns = Math.max(ls, s + firstDelta);
    var ne = s === e ? ns : Math.max(ns, e + totalDelta);
    field.setSelectionRange(ns, ne);
    return true;
  }

  // listContinuation inspects the current line at caret and returns an edit
  // descriptor { start, end, insert, caret } to continue or exit a markdown
  // list or blockquote on Enter, or null when the line is not a list/quote.
  function listContinuation(value, caret) {
    if (typeof value !== "string" || caret < 0 || caret > value.length) return null;
    var ls = value.lastIndexOf("\n", caret - 1) + 1;
    var le = value.indexOf("\n", caret);
    if (le < 0) le = value.length;
    var before = value.slice(ls, caret);
    var after = value.slice(caret, le);
    var m;

    // Task list: - [ ] / * [x] / + [X]
    m = /^([ \t]*)([-*+])([ \t]+)\[[ xX]\]([ \t]+)(.*)$/.exec(before);
    if (m) {
      if (!m[5].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var tIns = "\n" + m[1] + m[2] + m[3] + "[ ]" + m[4];
      return { start: caret, end: caret, insert: tIns, caret: caret + tIns.length };
    }
    // Unordered list: -, *, +
    m = /^([ \t]*)([-*+])([ \t]+)(.*)$/.exec(before);
    if (m) {
      if (!m[4].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var uIns = "\n" + m[1] + m[2] + m[3];
      return { start: caret, end: caret, insert: uIns, caret: caret + uIns.length };
    }
    // Ordered list: 1. or 1)
    m = /^([ \t]*)(\d+)([.)])([ \t]+)(.*)$/.exec(before);
    if (m) {
      if (!m[5].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var nIns = "\n" + m[1] + (parseInt(m[2], 10) + 1) + m[3] + m[4];
      return { start: caret, end: caret, insert: nIns, caret: caret + nIns.length };
    }
    // Blockquote: > ...
    m = /^([ \t]*(?:>[ \t]?)+)(.*)$/.exec(before);
    if (m) {
      if (!m[2].trim() && !after.trim()) return { start: ls, end: le, insert: "", caret: ls };
      var qPrefix = m[1];
      if (!/[ \t]$/.test(qPrefix)) qPrefix += " ";
      var qIns = "\n" + qPrefix;
      return { start: caret, end: caret, insert: qIns, caret: caret + qIns.length };
    }
    return null;
  }

  // formatCommandRows filters and sorts slash commands matching q.
  function formatCommandRows(list, q) {
    var needle = String(q || "").toLowerCase();
    var hits = (list || []).filter(function (c) {
      return !needle || (c.name || "").toLowerCase().indexOf(needle) >= 0 || (c.description || "").toLowerCase().indexOf(needle) >= 0;
    });
    hits.sort(function (a, b) {
      var ga = a.group || "", gb = b.group || "";
      if (ga !== gb) return ga === "User" ? -1 : gb === "User" ? 1 : ga.localeCompare(gb);
      var pa = (a.name || "").toLowerCase().indexOf(needle) === 0 ? 0 : 1;
      var pb = (b.name || "").toLowerCase().indexOf(needle) === 0 ? 0 : 1;
      return pa - pb || (a.name || "").localeCompare(b.name || "");
    });
    return hits;
  }

  function renderCommandRow(li, it) {
    var ownerDoc = (li && li.ownerDocument) || doc;
    var name = ownerDoc.createElement("span"), desc = ownerDoc.createElement("span");
    name.className = "wsm-name"; name.textContent = "/" + it.name;
    desc.className = "wsm-desc"; desc.textContent = it.description || "";
    li.appendChild(name); li.appendChild(desc);
    if (it.source) {
      var src = ownerDoc.createElement("span");
      src.className = "wsm-src"; src.textContent = it.source;
      li.appendChild(src);
    }
    li.title = "/" + it.name + (it.description ? " — " + it.description : "");
  }

  // defaultSlashToken matches a `/command` token at start of line or after
  // whitespace so paths like `foo/bar` never open the slash picker.
  function defaultSlashToken(v, caret) {
    var ls = v.lastIndexOf("\n", caret - 1) + 1;
    var le = v.indexOf("\n", caret);
    if (le < 0) le = v.length;
    var line = v.slice(ls, le), c = caret - ls;
    for (var i = line.lastIndexOf("/", c - 1); i >= 0; i = i > 0 ? line.lastIndexOf("/", i - 1) : -1) {
      if (i > 0 && !/\s/.test(line.charAt(i - 1))) continue;
      var body = line.slice(i + 1, c);
      if (/\s/.test(body)) return null;
      var tail = /^\S*/.exec(line.slice(c))[0];
      return { start: ls + i, end: ls + c + tail.length, q: body };
    }
    return null;
  }

  // --- WsInput core ---------------------------------------------------------
  var DEFAULTS = {
    markdown: true,
    capsules: true,
    mentions: true,
    topics: true,
    slash: true,
    imagePaste: false,
    longPaste: false,
    listContinuation: true,
    bangMode: false,
    autoGrow: true,
    voice: false
  };

  var instances = [];
  var voiceQueue = [];

  function attachVoice(api) {
    if (!api || !api.features || !api.features.voice) return;
    if (window.WsVoiceInput && typeof window.WsVoiceInput.attachField === "function") {
      api.voice = window.WsVoiceInput.attachField(api.field, api);
      return;
    }
    if (voiceQueue.indexOf(api) < 0) voiceQueue.push(api);
  }

  function flushVoice() {
    voiceQueue.slice().forEach(function (api) {
      var i = voiceQueue.indexOf(api);
      if (i >= 0) voiceQueue.splice(i, 1);
      if (api && !api.destroyed) attachVoice(api);
    });
  }

  function mergeFeatures(raw) {
    var out = {};
    Object.keys(DEFAULTS).forEach(function (k) {
      out[k] = raw && Object.prototype.hasOwnProperty.call(raw, k) ? !!raw[k] : DEFAULTS[k];
    });
    return out;
  }

  function clipboardImageFiles(dt) {
    var files = [];
    if (!dt) return files;
    if (dt.items) {
      for (var i = 0; i < dt.items.length; i++) {
        var it = dt.items[i];
        if (it.kind === "file" && /^image\//.test(it.type)) {
          var f = it.getAsFile && it.getAsFile();
          if (f) files.push(f);
        }
      }
    }
    if (!files.length && dt.files) {
      for (var j = 0; j < dt.files.length; j++) {
        if (/^image\//.test(dt.files[j].type)) files.push(dt.files[j]);
      }
    }
    return files;
  }

  function attach(target, options) {
    if (!target) return null;
    if (target.wsInput) {
      if (options) target.wsInput.updateConfig(options);
      return target.wsInput;
    }
    var opts = options || {};
    var ownerDoc = target.ownerDocument || doc;
    var field = target, ownsField = false;
    if (target.tagName !== "TEXTAREA") {
      field = ownerDoc.createElement("textarea");
      if (opts.className) field.className = opts.className;
      if (opts.placeholder) field.setAttribute("placeholder", opts.placeholder);
      if (opts.ariaLabel) field.setAttribute("aria-label", opts.ariaLabel);
      if (opts.rows) field.setAttribute("rows", String(opts.rows));
      if (typeof opts.initialValue === "string") field.value = opts.initialValue;
      target.appendChild(field);
      ownsField = true;
    }

    var rawFeatures = Object.assign({}, opts.features || {});
    if (Object.prototype.hasOwnProperty.call(opts, "voice")) rawFeatures.voice = opts.voice;
    var features = mergeFeatures(rawFeatures);
    var providers = opts.providers || {};
    var draftAdapter = opts.draftAdapter || null;
    var attachmentHandler = opts.attachmentHandler || null;

    // Per-instance isolated state
    var mentions = [];
    var mentionWas = field.value || "";
    var composing = false;
    var tabOut = false;
    var allowLineBreak = false;
    var mdRaf = 0;
    var growRaf = 0;
    var destroyed = false;

    var wsc = null;
    var mdEditor = null;
    var mentionPop = null;
    var topicPop = null;
    var slashPop = null;
    var ownsTopicPop = false;

    if (features.capsules && window.WsCapsules) {
      wsc = window.WsCapsules.attach(field);
    }

    function isBangActive() {
      if (!features.bangMode) return false;
      if (typeof opts.isBang === "function") return !!opts.isBang();
      return /^!/.test(field.value || "");
    }

    function currentKeymap() {
      var km = typeof opts.keymap === "function" ? opts.keymap() : opts.keymap;
      return km || "newline-only";
    }

    function editText(start, end, text) {
      var prevLen = field.value.length;
      if (typeof field.focus === "function") field.focus();
      if (typeof field.setSelectionRange === "function") field.setSelectionRange(start, end);
      var ok = false;
      try { ok = ownerDoc.execCommand && ownerDoc.execCommand("insertText", false, text); } catch (_) { ok = false; }
      if (!ok) {
        if (typeof field.setRangeText === "function") field.setRangeText(text, start, end, "end");
        else field.value = field.value.slice(0, start) + text + field.value.slice(end);
        field.dispatchEvent(new Event("input", { bubbles: true }));
      } else if (field.value.length === prevLen && (text.length !== end - start || field.value.slice(start, start + text.length) !== text)) {
        if (typeof field.setRangeText === "function") field.setRangeText(text, start, end, "end");
        else field.value = field.value.slice(0, start) + text + field.value.slice(end);
        field.dispatchEvent(new Event("input", { bubbles: true }));
      }
      if (wsc && text.indexOf("\n") >= 0 && typeof wsc.revealCaret === "function") wsc.revealCaret();
    }

    // --- Per-instance mention tracking ---
    function liveMention(m, v) {
      var before = m.start > 0 ? v.charAt(m.start - 1) : "", after = v.charAt(m.end);
      return v.slice(m.start, m.end) === m.text && (!before || /\s/.test(before)) && (!after || /\s/.test(after));
    }

    function shiftMentions(atOrDelta, maybeDelta) {
      var v = field.value || "";
      if (maybeDelta === undefined) {
        var d = atOrDelta;
        mentions = mentions.filter(function (m) { m.start += d; m.end += d; return liveMention(m, v); });
      } else {
        mentions = mentions.filter(function (m) {
          if (m.start >= atOrDelta) { m.start += maybeDelta; m.end += maybeDelta; }
          return liveMention(m, v);
        });
      }
      mentionWas = v;
      syncMarkdown();
    }

    function trackMentions() {
      var v = field.value || "", was = mentionWas, a = 0, s = 0;
      mentionWas = v;
      if (!mentions.length) return;
      while (a < was.length && a < v.length && was.charCodeAt(a) === v.charCodeAt(a)) a++;
      while (s < was.length - a && s < v.length - a && was.charCodeAt(was.length - 1 - s) === v.charCodeAt(v.length - 1 - s)) s++;
      var end = was.length - s, d = v.length - was.length;
      mentions = mentions.filter(function (m) {
        if (end <= m.start) { m.start += d; m.end += d; } else if (a < m.end) return false;
        return liveMention(m, v);
      });
    }

    function sendText(raw) {
      var v = typeof raw === "string" ? raw : (field.value || "");
      return stripMentions(v, mentions);
    }

    // --- Per-instance markdown rendering via WsMdEditor seam ---
    function clearMarkdown() {
      if (!mdEditor) return;
      if (typeof mdEditor.clear === "function") mdEditor.clear();
      else if (typeof mdEditor.reconcile === "function") mdEditor.reconcile();
    }

    function reconcileMarkdown() {
      if (destroyed || !features.markdown || !mdEditor) return;
      if (typeof mdEditor.reconcile === "function") mdEditor.reconcile();
    }

    function syncMarkdown() {
      if (destroyed || !features.markdown || !mdEditor) return;
      if (composing) return;
      if (typeof mdEditor.schedule === "function") {
        mdEditor.schedule();
        return;
      }
      if (mdRaf) return;
      var raf = typeof requestAnimationFrame === "function" ? requestAnimationFrame : function (fn) { return setTimeout(fn, 0); };
      mdRaf = raf(function () {
        mdRaf = 0;
        if (!destroyed && !composing) reconcileMarkdown();
      });
    }

    function attachMarkdown() {
      if (mdEditor) {
        if (typeof mdEditor.detach === "function") mdEditor.detach();
        mdEditor = null;
      }
      if (!features.markdown || !window.WsMdEditor || typeof window.WsMdEditor.attach !== "function") return;
      var editorEl = wsc && wsc.editor ? wsc.editor : null;
      if (!editorEl) return;
      mdEditor = window.WsMdEditor.attach(editorEl, {
        getMentions: function () { return mentions; },
        isBang: isBangActive,
        isComposing: function () { return composing; },
        onReconciled: opts.onMarkdownReconciled
      });
    }

    // --- Per-instance autoGrow (no layout thrashing on .wsc-backing) ---
    function syncGrow() {
      if (!features.autoGrow) return;
      if (field.classList && field.classList.contains("wsc-backing")) return;
      if (typeof CSS !== "undefined" && CSS.supports && CSS.supports("field-sizing", "content")) return;
      if (growRaf) return;
      var raf = typeof requestAnimationFrame === "function" ? requestAnimationFrame : function (fn) { return setTimeout(fn, 0); };
      growRaf = raf(function () {
        growRaf = 0;
        if (destroyed || (field.classList && field.classList.contains("wsc-backing"))) return;
        field.style.height = "auto";
        if (field.scrollHeight) field.style.height = field.scrollHeight + "px";
      });
    }

    // --- Per-instance popups (@, #, /) ---
    function attachMentionPopup() {
      if (mentionPop) { mentionPop.destroy(); mentionPop = null; }
      if (!features.mentions || !window.wsMention || typeof providers.files !== "function") return;
      mentionPop = window.wsMention.attach(field, {
        label: opts.mentionLabel || wsT("js.mention.label"),
        token: function (v, c) {
          if (!features.mentions || isBangActive()) return null;
          return window.wsMention.tokenAt(v, c);
        },
        source: providers.files,
        insert: editText,
        onPick: function (m) {
          trackMentions();
          if (!m.dir) {
            mentions = mentions.filter(function (x) { return x.end <= m.start || x.start >= m.end; });
            mentions.push({ start: m.start, end: m.end, text: m.text });
          }
          if (typeof opts.onMentionPick === "function") opts.onMentionPick(m, api);
          syncMarkdown();
        }
      });
    }

    function attachTopicPopup() {
      if (topicPop && ownsTopicPop) { topicPop.destroy(); topicPop = null; ownsTopicPop = false; }
      if (!features.topics || !window.WsCapsules || typeof providers.topics !== "function") return;
      if (wsc && wsc.topicPop) {
        topicPop = wsc.topicPop;
        ownsTopicPop = false;
        return;
      }
      topicPop = window.WsCapsules.topicPicker(field, providers.topics);
      ownsTopicPop = !!topicPop;
    }

    function attachSlashPopup() {
      if (slashPop) { slashPop.destroy(); slashPop = null; }
      if (!features.slash || !window.wsMention || typeof providers.commands !== "function") return;
      var slashTok = opts.slashToken || defaultSlashToken;
      var slashOpts = {
        label: opts.slashLabel || wsT("js.cmd.label"),
        max: opts.slashMax || 50,
        token: function (v, c) {
          if (!features.slash || isBangActive()) return null;
          return slashTok(v, c);
        },
        source: function (q, done) {
          providers.commands(q, function (rows) {
            done(opts.preformattedCommands ? (rows || []) : formatCommandRows(rows || [], q));
          });
        },
        render: opts.renderCommand || renderCommandRow,
        choose: function (it, at) {
          if (typeof opts.onSlashPick === "function") {
            opts.onSlashPick(it, at, api);
            return;
          }
          var v = field.value || "";
          var spaced = /\s/.test(v.charAt(at.end));
          var text = "/" + it.name + (spaced ? "" : " ");
          editText(at.start, at.end, text);
          var caret = at.start + ("/" + it.name).length + (spaced ? 0 : 1);
          if (typeof field.setSelectionRange === "function") field.setSelectionRange(caret, caret);
        }
      };
      if (typeof opts.slashExact === "function") slashOpts.exact = opts.slashExact;
      slashPop = window.wsMention.attach(field, slashOpts);
    }

    function handlePopupKey(e) {
      if (slashPop && slashPop.key(e)) return true;
      if (mentionPop && mentionPop.key(e)) return true;
      if (topicPop && topicPop.key(e)) return true;
      return false;
    }

    function closePopups() {
      if (slashPop) slashPop.close();
      if (mentionPop) mentionPop.close();
      if (topicPop) topicPop.close();
    }

    function syncPopups() {
      if (slashPop) slashPop.sync();
      if (mentionPop) mentionPop.sync();
      if (topicPop) topicPop.sync();
    }

    // --- Event handlers ---
    function onInput() {
      tabOut = false;
      trackMentions();
      syncGrow();
      syncMarkdown();
      if (draftAdapter && typeof draftAdapter.set === "function") draftAdapter.set(field.value);
      if (typeof opts.onChange === "function") opts.onChange(field.value, api);
    }

    function onCompStart() {
      composing = true;
    }

    function onCompEnd() {
      composing = false;
      syncMarkdown();
    }

    function onBeforeInput(e) {
      if (e.inputType !== "insertParagraph" && e.inputType !== "insertLineBreak") return;
      var km = currentKeymap();
      if ((km === "enter-send" || km === "enter-save") && !allowLineBreak) e.preventDefault();
      allowLineBreak = false;
    }

    function onKeyDown(e) {
      if (e.key === "Enter" || e.code === "Enter" || e.code === "NumpadEnter") allowLineBreak = !!(e.shiftKey || e.altKey);
      else if (e.key !== "Shift" && e.key !== "Alt") allowLineBreak = false;
      if (e.defaultPrevented || e.isComposing || e.keyCode === 229) return;
      if (typeof opts.onBeforeKeyDown === "function" && opts.onBeforeKeyDown(e, api) === true) return;

      if (handlePopupKey(e)) return;

      if (e.key === "Escape") {
        if (typeof opts.onEscape === "function" && opts.onEscape(e, api) === true) return;
        if (opts.smartTab) tabOut = true;
        if (typeof opts.onCancel === "function") {
          e.preventDefault();
          opts.onCancel(api, e);
        }
        return;
      }

      if (e.key === "Tab" && !e.ctrlKey && !e.metaKey && !e.altKey) {
        if (opts.smartTab) {
          if (tabOut) { tabOut = false; return; }
          e.preventDefault();
          indentLines(field, e.shiftKey, editText);
        }
        return;
      }

      tabOut = false;

      if (e.key === "Enter") {
        var km = currentKeymap();
        var hasMod = !!(e.ctrlKey || e.metaKey);
        var plainEnter = !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey;
        var shiftEnter = e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey;
        var submit = false;
        if (km === "enter-send" || km === "enter-save") {
          submit = plainEnter || (hasMod && !e.altKey && opts.modEnterSubmits !== false);
        } else if (km === "mod-send") {
          submit = hasMod && !e.altKey;
        }
        if (submit && typeof opts.onSubmit === "function") {
          e.preventDefault();
          if (!e.repeat) opts.onSubmit(api, e);
          return;
        }
        // Newline path (Shift+Enter in enter-send/enter-save, plain Enter in mod-send/newline-only)
        var insertingNewline = (km === "enter-send" || km === "enter-save") ? shiftEnter : (km === "mod-send" || km === "newline-only") ? (plainEnter || shiftEnter) : false;
        if (insertingNewline && features.listContinuation && field.selectionStart === field.selectionEnd) {
          var cont = listContinuation(field.value || "", field.selectionStart);
          if (cont) {
            e.preventDefault();
            editText(cont.start, cont.end, cont.insert);
            if (typeof field.setSelectionRange === "function") field.setSelectionRange(cont.caret, cont.caret);
            return;
          }
        }
      }

      if (typeof opts.onKeyDown === "function") opts.onKeyDown(e, api);
    }

    function onPaste(e) {
      if (e.defaultPrevented) return;
      var dt = e.clipboardData;
      if (!dt) return;
      if (features.imagePaste && attachmentHandler && typeof attachmentHandler.onImagePaste === "function") {
        var imgs = clipboardImageFiles(dt);
        if (imgs.length) {
          if (attachmentHandler.onImagePaste(imgs, e, api) !== false) return;
        }
      }
      if (features.longPaste && attachmentHandler && typeof attachmentHandler.onLongPaste === "function") {
        var plain = dt.getData ? dt.getData("text/plain") : "";
        if (plain && attachmentHandler.onLongPaste(plain, e, api) === true) return;
      }
    }

    function onBlur() {
      tabOut = false;
      allowLineBreak = false;
      if (draftAdapter && typeof draftAdapter.persist === "function") draftAdapter.persist();
      if (typeof opts.onBlur === "function") opts.onBlur(api);
    }

    var api = {
      field: field,
      voice: null,
      destroyed: false,
      target: target,
      editor: wsc ? wsc.editor : field,
      wsc: wsc,
      features: features,
      allowsTopic: function () { return features.topics && !isBangActive(); },
      allowsMention: function () { return features.mentions && !isBangActive(); },
      allowsSlash: function () { return features.slash && !isBangActive(); },
      isBang: isBangActive,
      getValue: function () { return field.value || ""; },
      setValue: function (val, optsSet) {
        var next = String(val == null ? "" : val);
        field.value = next;
        mentionWas = next;
        if (!(optsSet && optsSet.keepMentions)) mentions = [];
        syncGrow();
        syncMarkdown();
        if (draftAdapter && typeof draftAdapter.set === "function") draftAdapter.set(next);
      },
      getSelection: function () { return [field.selectionStart || 0, field.selectionEnd || 0]; },
      setSelection: function (a, b) {
        if (typeof field.setSelectionRange === "function") field.setSelectionRange(a, b == null ? a : b);
      },
      focus: function () { if (typeof field.focus === "function") field.focus(); },
      blur: function () { if (typeof field.blur === "function") field.blur(); },
      editText: editText,
      getMentions: function () { return mentions.slice(); },
      setMentions: function (list) {
        mentions = Array.isArray(list) ? list.map(function (m) { return Object.assign({}, m); }) : [];
        mentionWas = field.value || "";
        syncMarkdown();
      },
      shiftMentions: shiftMentions,
      clearMentions: function () { mentions = []; mentionWas = field.value || ""; syncMarkdown(); },
      trackMentions: trackMentions,
      sendText: sendText,
      syncMarkdown: syncMarkdown,
      reconcileMarkdown: reconcileMarkdown,
      clearMarkdown: clearMarkdown,
      syncGrow: syncGrow,
      syncPopups: syncPopups,
      closePopups: closePopups,
      handlePopupKey: handlePopupKey,
      handleIndent: function (out) { return indentLines(field, !!out, editText); },
      getPopups: function () { return { mention: mentionPop, topic: topicPop, slash: slashPop }; },
      updateConfig: function (nextOpts) {
        if (!nextOpts) return;
        opts = Object.assign({}, opts, nextOpts);
        if (nextOpts.features) {
          var prevFeatures = features;
          var changedFeatures = Object.assign({}, prevFeatures, nextOpts.features);
          if (Object.prototype.hasOwnProperty.call(nextOpts, "voice")) changedFeatures.voice = nextOpts.voice;
          features = mergeFeatures(changedFeatures);
          api.features = features;
        }
        if (!nextOpts.features && Object.prototype.hasOwnProperty.call(nextOpts, "voice")) {
          features = mergeFeatures(Object.assign({}, features, { voice: nextOpts.voice }));
          api.features = features;
        }
        if (nextOpts.providers) providers = Object.assign({}, providers, nextOpts.providers);
        if (Object.prototype.hasOwnProperty.call(nextOpts, "draftAdapter")) draftAdapter = nextOpts.draftAdapter;
        if (Object.prototype.hasOwnProperty.call(nextOpts, "attachmentHandler")) attachmentHandler = nextOpts.attachmentHandler;
        attachMarkdown();
        attachMentionPopup();
        attachTopicPopup();
        attachSlashPopup();
        syncMarkdown();
        if (features.voice && !api.voice) attachVoice(api);
        if (!features.voice && api.voice) { api.voice.destroy(); api.voice = null; }
      },
      destroy: function () {
        if (destroyed) return;
        destroyed = true;
        api.destroyed = true;
        var voiceIndex = voiceQueue.indexOf(api);
        if (voiceIndex >= 0) voiceQueue.splice(voiceIndex, 1);
        if (api.voice) { api.voice.destroy(); api.voice = null; }
        if (typeof field.removeEventListener === "function") {
          field.removeEventListener("input", onInput);
          field.removeEventListener("compositionstart", onCompStart);
          field.removeEventListener("compositionend", onCompEnd);
          if (!opts.managedKeydown) {
            field.removeEventListener("beforeinput", onBeforeInput);
            field.removeEventListener("keydown", onKeyDown);
          }
          field.removeEventListener("paste", onPaste);
          field.removeEventListener("blur", onBlur);
        }
        if (mdEditor) { mdEditor.detach(); mdEditor = null; }
        if (mentionPop) { mentionPop.destroy(); mentionPop = null; }
        if (topicPop && ownsTopicPop) { topicPop.destroy(); topicPop = null; ownsTopicPop = false; }
        if (slashPop) { slashPop.destroy(); slashPop = null; }
        if (field.wsInput === api) delete field.wsInput;
        if (target !== field && target.wsInput === api) delete target.wsInput;
        if (ownsField && field.parentNode) field.parentNode.removeChild(field);
        var idx = instances.indexOf(api);
        if (idx >= 0) instances.splice(idx, 1);
      }
    };

    field.wsInput = api;
    if (target !== field) target.wsInput = api;

    field.addEventListener("input", onInput);
    field.addEventListener("compositionstart", onCompStart);
    field.addEventListener("compositionend", onCompEnd);
    if (!opts.managedKeydown) {
      field.addEventListener("beforeinput", onBeforeInput);
      field.addEventListener("keydown", onKeyDown);
    }
    field.addEventListener("paste", onPaste);
    field.addEventListener("blur", onBlur);

    attachMarkdown();
    attachMentionPopup();
    attachTopicPopup();
    attachSlashPopup();
    if (field.value) reconcileMarkdown();

    instances.push(api);
    attachVoice(api);
    return api;
  }

  function get(target) {
    return target && target.wsInput ? target.wsInput : null;
  }

  function sweep(root) {
    for (var i = instances.length - 1; i >= 0; i--) {
      var inst = instances[i];
      if (!inst || !inst.field) { instances.splice(i, 1); continue; }
      if (inst.field.isConnected === false || (root && root.contains && root.contains(inst.field))) {
        inst.destroy();
      }
    }
  }

  function liveMentionSpan(m, v) {
    if (!m || typeof m.text !== "string") return false;
    var before = m.start > 0 ? v.charAt(m.start - 1) : "", after = v.charAt(m.end);
    return v.slice(m.start, m.end) === m.text && (!before || /\s/.test(before)) && (!after || /\s/.test(after));
  }

  function stripMentions(raw, list) {
    var v = String(raw || ""), out = "", at = 0;
    if (!Array.isArray(list) || !list.length) return v;
    list.slice().sort(function (a, b) { return a.start - b.start; }).forEach(function (m) {
      if (m.start < at || !liveMentionSpan(m, v)) return;
      out += v.slice(at, m.start) + m.text.slice(1);
      at = m.end;
    });
    return out + v.slice(at);
  }

  window.WsInput = {
    attach: attach,
    get: get,
    sweep: sweep,
    flushVoice: flushVoice,
    DEFAULTS: DEFAULTS,
    indentLines: indentLines,
    listContinuation: listContinuation,
    formatCommandRows: formatCommandRows,
    renderCommandRow: renderCommandRow,
    defaultSlashToken: defaultSlashToken,
    stripMentions: stripMentions
  };
})();
