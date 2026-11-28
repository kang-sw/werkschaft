(function (global) {
  "use strict";

  // ============================================================================
  // Floating KakaoTalk-Style Companion Messenger Popup
  // ============================================================================
  // Mounted directly under document.body (position: fixed) so .rail overflow
  // never clips it and Variant D resizable rail widths (220px..360px+) anchor
  // cleanly beside #rail-companion-slot.
  // ============================================================================

  const POPUP_DEFAULT_W = 360;
  const POPUP_DEFAULT_H = 540;
  const VIEWPORT_MARGIN = 12;
  const ANCHOR_GAP = 12;
  const SVG_NS = "http://www.w3.org/2000/svg";

  function clamp(v, lo, hi) {
    return Math.min(Math.max(v, lo), hi);
  }

  function t(key, fallback, ...args) {
    let text = fallback;
    if (typeof global.wsT === "function") {
      const resolved = global.wsT(key, ...args);
      if (resolved && resolved !== key) text = resolved;
    }
    if (args.length && text === fallback) {
      let idx = 0;
      text = text.replace(/%[sd]/g, () => String(args[idx++] ?? ""));
    }
    return text;
  }

  // Pure viewport-clamped anchor math (exported for unit tests).
  function computePopupRect(anchorRect, viewport, popupSize) {
    const vw = Math.max(320, Number(viewport?.width) || 1280);
    const vh = Math.max(320, Number(viewport?.height) || 800);
    const maxW = Math.max(260, vw - VIEWPORT_MARGIN * 2);
    const maxH = Math.max(280, vh - VIEWPORT_MARGIN * 2);
    const width = Math.min(Number(popupSize?.width) || POPUP_DEFAULT_W, maxW);
    const height = Math.min(Number(popupSize?.height) || POPUP_DEFAULT_H, maxH);

    let left = VIEWPORT_MARGIN;
    let top = vh - height - VIEWPORT_MARGIN;
    let caretSide = "left";
    let caretBottom = 96;

    if (anchorRect && Number.isFinite(anchorRect.right) && Number.isFinite(anchorRect.bottom) && anchorRect.width > 0) {
      const rightSpace = vw - anchorRect.right - ANCHOR_GAP - VIEWPORT_MARGIN;
      const leftSpace = anchorRect.left - ANCHOR_GAP - VIEWPORT_MARGIN;
      if (rightSpace >= width || rightSpace >= leftSpace) {
        left = clamp(anchorRect.right + ANCHOR_GAP, VIEWPORT_MARGIN, vw - width - VIEWPORT_MARGIN);
        caretSide = "left";
      } else {
        left = clamp(anchorRect.left - ANCHOR_GAP - width, VIEWPORT_MARGIN, vw - width - VIEWPORT_MARGIN);
        caretSide = "right";
      }
      const desiredBottom = Math.min(vh - VIEWPORT_MARGIN, Math.max(anchorRect.bottom, VIEWPORT_MARGIN + height));
      top = clamp(desiredBottom - height, VIEWPORT_MARGIN, vh - height - VIEWPORT_MARGIN);
      const anchorMidY = anchorRect.top + (anchorRect.height || 0) * 0.5;
      const popupBottom = top + height;
      caretBottom = clamp(Math.round(popupBottom - anchorMidY), 28, Math.max(28, height - 36));
    }

    return {
      left: Math.round(left),
      top: Math.round(top),
      width: Math.round(width),
      height: Math.round(height),
      caretSide,
      caretBottom: Math.round(caretBottom)
    };
  }

  function makeSvg(doc, paths) {
    const svg = doc.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "ws-cm-i");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    for (const spec of paths) {
      const el = doc.createElementNS(SVG_NS, spec.tag);
      for (const [k, v] of Object.entries(spec.attrs)) el.setAttribute(k, v);
      svg.append(el);
    }
    return svg;
  }

  const ICONS = {
    layers: [
      { tag: "path", attrs: { d: "m12 3 9 4.5-9 4.5-9-4.5z" } },
      { tag: "path", attrs: { d: "m3 12 9 4.5 9-4.5" } },
      { tag: "path", attrs: { d: "m3 16.5 9 4.5 9-4.5" } }
    ],
    pin: [
      { tag: "path", attrs: { d: "M12 17v5" } },
      { tag: "path", attrs: { d: "M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 1 1 0 0 0 1-1V4a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v1a1 1 0 0 0 1 1 1 1 0 0 1 1 1z" } }
    ],
    gear: [
      { tag: "circle", attrs: { cx: "12", cy: "12", r: "3" } },
      { tag: "path", attrs: { d: "M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" } }
    ],
    close: [
      { tag: "path", attrs: { d: "M6 6l12 12M18 6 6 18" } }
    ],
    send: [
      { tag: "path", attrs: { d: "M12 19V5" } },
      { tag: "path", attrs: { d: "m6 11 6-6 6 6" } }
    ],
    shield: [
      { tag: "path", attrs: { d: "M12 3 5 6v6c0 4.5 3 7.8 7 9 4-1.2 7-4.5 7-9V6z" } },
      { tag: "path", attrs: { d: "M12 9v4" } },
      { tag: "circle", attrs: { cx: "12", cy: "16", r: "0.8", fill: "currentColor", stroke: "none" } }
    ],
    avatar: [
      { tag: "circle", attrs: { cx: "12", cy: "12", r: "9" } },
      { tag: "circle", attrs: { cx: "9.2", cy: "10.6", r: "1.1", fill: "currentColor", stroke: "none" } },
      { tag: "circle", attrs: { cx: "14.8", cy: "10.6", r: "1.1", fill: "currentColor", stroke: "none" } },
      { tag: "path", attrs: { d: "M9 14.5c.8 1.2 1.9 1.8 3 1.8s2.2-.6 3-1.8" } }
    ]
  };

  function formatTime(ms) {
    if (!Number.isFinite(ms) || ms <= 0) return "";
    try {
      const d = new Date(ms);
      return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    } catch (_) {
      return "";
    }
  }

  function statusForSnapshot(snap) {
    const st = snap?.state || "laptop_ambient";
    const unreadAgent = Number(snap?.unreadAgent) || 0;
    if (snap?.pendingApproval && snap.pendingApproval.requestId) {
      return {
        dot: "approval",
        subtitle: t("js.companion.messenger.status.approval_paused", "Paused — waiting for 1-click approval"),
        typing: ""
      };
    }
    if (st === "phone_active") {
      return {
        dot: "phone",
        subtitle: t("js.companion.messenger.status.phone", "Checking smartphone with one hand"),
        typing: t("js.companion.messenger.typing.phone", "Checking smartphone…")
      };
    }
    if (st === "laptop_work") {
      return {
        dot: "work",
        subtitle: t("js.companion.messenger.status.work", "Handling your request on laptop"),
        typing: t("js.companion.messenger.typing.work", "Working on laptop…")
      };
    }
    if (snap?.unavailable) {
      return {
        dot: "unavailable",
        subtitle: t("js.companion.messenger.status.unavailable", "Companion is temporarily unavailable — send a message to retry"),
        typing: ""
      };
    }
    if (st === "laptop_sleepy") {
      return {
        dot: "sleepy",
        subtitle: t("js.companion.messenger.status.sleepy", "Dozing off… knock the glass to wake up"),
        typing: ""
      };
    }
    if (unreadAgent > 0) {
      return {
        dot: "buzz",
        subtitle: t("js.companion.messenger.status.buzz", "Smartphone buzz! Waiting to check"),
        typing: t("js.companion.messenger.typing.buzz", "Smartphone buzzing…")
      };
    }
    if (snap?.phoneFaceDown) {
      return {
        dot: "facedown",
        subtitle: t("js.companion.messenger.status.facedown", "Bleeh! Phone placed face-down"),
        typing: ""
      };
    }
    return {
      dot: "laptop",
      subtitle: t("js.companion.messenger.status.laptop", "Working on laptop · send a message to ping phone"),
      typing: ""
    };
  }

  function toneMeta(tone) {
    const k = typeof tone === "string" ? tone.trim().toLowerCase() : "";
    if (!k) return null;
    if (k === "merong" || k === "wink") return { cls: "mood-merong", label: "😜 " + k };
    if (k === "laugh" || k === "happy") return { cls: "mood-laugh", label: "😄 " + k };
    if (k === "annoyed") return { cls: "mood-annoyed", label: "😤 " + k };
    if (k === "inspect" || k === "focused") return { cls: "mood-inspect", label: "👀 " + k };
    if (k === "ok" || k === "done") return { cls: "mood-ok", label: "✓ " + k };
    return { cls: "", label: k };
  }

  // ==========================================================================
  // Safe Markdown + badge rendering for bubbles.
  // The tree is built only with createElement / createTextNode / textContent;
  // user or companion text never reaches innerHTML, so markup such as
  // <img onerror> or <script> stays inert literal text. Links are limited to
  // http(s)/mailto. Supported: paragraphs, headings, fenced code, quotes,
  // flat lists, hr, **bold**, *italic*, ~~strike~~, `code`, [links], bare
  // URLs, <mailbox .../> tags and @mention badges.
  // ==========================================================================
  const MD_MAX_DEPTH = 4;
  const SAFE_URL_RE = /^(?:https?:\/\/|mailto:)[^\s<>"']+$/i;
  const BARE_URL_RE = /^https?:\/\/[^\s<>"'`]+/i;
  const MENTION_RE = /^@[A-Za-z0-9_][A-Za-z0-9_./:-]*/;

  function mdText(doc, s) {
    if (typeof doc.createTextNode === "function") return doc.createTextNode(s);
    const span = doc.createElement("span");
    span.textContent = s;
    return span;
  }

  function mdEl(doc, tag, cls) {
    const el = doc.createElement(tag);
    if (cls) el.className = cls;
    return el;
  }

  function safeHref(url) {
    const u = String(url || "").replace(/[\u0000-\u001f\u007f\s]+/g, "");
    return SAFE_URL_RE.test(u) ? u : "";
  }

  function unescapeXml(s) {
    return String(s).replace(/&(amp|lt|gt|quot);/g, (m, n) => (n === "amp" ? "&" : n === "lt" ? "<" : n === "gt" ? ">" : '"'));
  }

  // Parses a <mailbox .../> tag at text[start]. Prefers the dashboard's own
  // WsCapsules parser (same grammar the composer capsules use); falls back to
  // a strict self-contained one when capsules.js is absent.
  function parseMailboxAt(text, start) {
    const caps = global.WsCapsules;
    if (caps && typeof caps.parseMailboxTag === "function") {
      const hit = caps.parseMailboxTag(text, start);
      if (!hit) return null;
      const label = typeof caps.formatMailboxLabel === "function" ? caps.formatMailboxLabel(hit.tag, {}) : (hit.tag.label || hit.tag.id);
      const title = typeof caps.formatMailboxTooltip === "function" ? caps.formatMailboxTooltip(hit.tag) : "id: " + hit.tag.id;
      return { end: hit.end, label, title };
    }
    const m = /^<mailbox\s+((?:[a-z][a-z0-9_-]*="[^"<>\r\n]*"\s*)+)\/>/.exec(text.slice(start, start + 600));
    if (!m) return null;
    const attrs = {};
    m[1].replace(/([a-z][a-z0-9_-]*)="([^"]*)"/g, (_, k, v) => { attrs[k] = unescapeXml(v); return ""; });
    if (!attrs.id) return null;
    return { end: start + m[0].length, label: attrs.label || attrs.id.slice(0, 9), title: "id: " + attrs.id };
  }

  function mdBadge(doc, kind, label, title) {
    const b = mdEl(doc, "span", "ws-cm-badge " + kind);
    b.textContent = label;
    if (title) b.title = title;
    return b;
  }

  function findClose(text, from, delim) {
    for (let k = text.indexOf(delim, from); k >= 0; k = text.indexOf(delim, k + 1)) {
      if (k > from && !/\s/.test(text.charAt(k - 1))) return k;
    }
    return -1;
  }

  function renderInline(doc, parent, text, depth) {
    let buf = "";
    const flush = () => {
      if (buf) { parent.append(mdText(doc, buf)); buf = ""; }
    };
    const n = text.length;
    for (let i = 0; i < n;) {
      const ch = text.charAt(i);
      const prev = i > 0 ? text.charAt(i - 1) : "";
      if (ch === "\\" && i + 1 < n && /[!-/:-@[-`{-~]/.test(text.charAt(i + 1))) {
        buf += text.charAt(i + 1); i += 2; continue;
      }
      if (ch === "\n") { flush(); parent.append(doc.createElement("br")); i++; continue; }
      if (ch === "`") {
        let run = 1;
        while (text.charAt(i + run) === "`") run++;
        const close = text.indexOf("`".repeat(run), i + run);
        if (close > i + run) {
          flush();
          const code = mdEl(doc, "code", "ws-cm-md-code");
          code.textContent = text.slice(i + run, close).replace(/^ (.*) $/s, "$1");
          parent.append(code);
          i = close + run; continue;
        }
        buf += "`".repeat(run); i += run; continue;
      }
      if (ch === "<" && text.startsWith("<mailbox ", i)) {
        const mb = parseMailboxAt(text, i);
        if (mb) { flush(); parent.append(mdBadge(doc, "mailbox", mb.label, mb.title)); i = mb.end; continue; }
      }
      if ((ch === "[" || (ch === "!" && text.charAt(i + 1) === "[")) && depth < MD_MAX_DEPTH) {
        const open = ch === "!" ? i + 1 : i;
        const closeBr = text.indexOf("]", open + 1);
        if (closeBr > open + 1 && text.charAt(closeBr + 1) === "(") {
          const closeP = text.indexOf(")", closeBr + 2);
          if (closeP > closeBr + 2) {
            const label = text.slice(open + 1, closeBr);
            const href = safeHref(text.slice(closeBr + 2, closeP).trim().split(/\s+/)[0]);
            flush();
            if (href) {
              const a = mdEl(doc, "a", "ws-cm-md-link");
              a.setAttribute("href", href);
              a.setAttribute("target", "_blank");
              a.setAttribute("rel", "noopener noreferrer");
              renderInline(doc, a, ch === "!" ? (label || href) : label, depth + 1);
              parent.append(a);
            } else {
              parent.append(mdText(doc, label));
            }
            i = closeP + 1; continue;
          }
        }
      }
      if ((ch === "h" || ch === "H") && !/[A-Za-z0-9]/.test(prev)) {
        const bare = BARE_URL_RE.exec(text.slice(i));
        if (bare) {
          const url = bare[0].replace(/[.,;:!?)\]]+$/, "");
          const href = safeHref(url);
          if (href) {
            flush();
            const a = mdEl(doc, "a", "ws-cm-md-link");
            a.setAttribute("href", href);
            a.setAttribute("target", "_blank");
            a.setAttribute("rel", "noopener noreferrer");
            a.textContent = url;
            parent.append(a);
            i += url.length; continue;
          }
        }
      }
      if (ch === "@" && (!prev || /[\s(]/.test(prev))) {
        const m = MENTION_RE.exec(text.slice(i));
        if (m) {
          const tok = m[0].replace(/[.,:;!?)]+$/, "");
          if (tok.length > 1) {
            flush();
            parent.append(mdBadge(doc, "mention", tok, ""));
            i += tok.length; continue;
          }
        }
      }
      if ((ch === "*" || ch === "_" || ch === "~") && depth < MD_MAX_DEPTH) {
        let delim = "";
        if (ch === "~") delim = text.startsWith("~~", i) ? "~~" : "";
        else if (text.startsWith(ch + ch, i)) delim = ch + ch;
        else delim = ch;
        const intraword = ch === "_" && /[A-Za-z0-9]/.test(prev);
        if (delim && !intraword && i + delim.length < n && !/\s/.test(text.charAt(i + delim.length))) {
          const close = findClose(text, i + delim.length, delim);
          const after = close >= 0 ? text.charAt(close + delim.length) : "";
          if (close > 0 && !(ch === "_" && /[A-Za-z0-9]/.test(after))) {
            flush();
            const tag = delim === "~~" ? "del" : delim.length === 2 ? "strong" : "em";
            const el = doc.createElement(tag);
            renderInline(doc, el, text.slice(i + delim.length, close), depth + 1);
            parent.append(el);
            i = close + delim.length; continue;
          }
        }
      }
      buf += ch; i++;
    }
    flush();
  }

  const BLOCK_START_RE = /^(?: {0,3}(?:#{1,6}\s|>|```|~~~|[-*+]\s|\d{1,9}[.)]\s)|[ \t]*(?:[-*_][ \t]*){3,}$)/;

  function renderBlocks(doc, parent, text, depth) {
    const lines = String(text).replace(/\r\n?/g, "\n").split("\n");
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      if (!line.trim()) { i++; continue; }
      const fence = /^ {0,3}(```+|~~~+)\s*([\w+#.-]*)/.exec(line);
      if (fence) {
        const mark = fence[1];
        const body = [];
        i++;
        while (i < lines.length && !new RegExp("^ {0,3}" + mark.charAt(0) + "{" + mark.length + ",}\\s*$").test(lines[i])) body.push(lines[i++]);
        i++;
        const pre = mdEl(doc, "pre", "ws-cm-md-pre");
        const code = doc.createElement("code");
        code.textContent = body.join("\n");
        if (fence[2]) pre.setAttribute("data-lang", fence[2].slice(0, 24));
        pre.append(code);
        parent.append(pre);
        continue;
      }
      const head = /^ {0,3}(#{1,6})\s+(.*?)(?:\s+#+)?\s*$/.exec(line);
      if (head) {
        const h = mdEl(doc, "div", "ws-cm-md-h ws-cm-md-h" + head[1].length);
        h.setAttribute("role", "heading");
        h.setAttribute("aria-level", String(head[1].length));
        renderInline(doc, h, head[2], 0);
        parent.append(h);
        i++; continue;
      }
      if (/^[ \t]*(?:[-*_][ \t]*){3,}$/.test(line)) {
        parent.append(doc.createElement("hr"));
        i++; continue;
      }
      if (/^ {0,3}>/.test(line)) {
        const quoted = [];
        while (i < lines.length && /^ {0,3}>/.test(lines[i])) quoted.push(lines[i++].replace(/^ {0,3}>[ \t]?/, ""));
        const bq = doc.createElement("blockquote");
        if (depth < MD_MAX_DEPTH) renderBlocks(doc, bq, quoted.join("\n"), depth + 1);
        else bq.textContent = quoted.join("\n");
        parent.append(bq);
        continue;
      }
      const li = /^( *)([-*+]|\d{1,9}[.)])\s+(.*)$/.exec(line);
      if (li) {
        const ordered = /\d/.test(li[2]);
        const list = doc.createElement(ordered ? "ol" : "ul");
        if (ordered && li[2].replace(/\D/g, "") !== "1") list.setAttribute("start", String(parseInt(li[2], 10)));
        while (i < lines.length) {
          const m = /^( *)([-*+]|\d{1,9}[.)])\s+(.*)$/.exec(lines[i]);
          if (!m || /\d/.test(m[2]) !== ordered) break;
          const itemLines = [m[3]];
          i++;
          while (i < lines.length && lines[i].trim() && /^ {2,}\S/.test(lines[i]) && !/^ *([-*+]|\d{1,9}[.)])\s+/.test(lines[i])) itemLines.push(lines[i++].trim());
          const item = doc.createElement("li");
          let body = itemLines.join("\n");
          const task = /^\[([ xX])\]\s+/.exec(body);
          if (task) {
            body = body.slice(task[0].length);
            item.className = "ws-cm-md-task";
            item.append(mdText(doc, task[1] === " " ? "☐ " : "☑ "));
          }
          renderInline(doc, item, body, 0);
          list.append(item);
        }
        parent.append(list);
        continue;
      }
      const para = [line];
      i++;
      while (i < lines.length && lines[i].trim() && !BLOCK_START_RE.test(lines[i])) para.push(lines[i++]);
      const p = mdEl(doc, "p", "ws-cm-md-p");
      renderInline(doc, p, para.join("\n"), 0);
      parent.append(p);
    }
  }

  // Replaces container's content with the rendered Markdown of text.
  function renderMarkdownInto(doc, container, text) {
    const frag = doc.createElement("div");
    renderBlocks(doc, frag, String(text == null ? "" : text), 0);
    container.replaceChildren(...(frag.children || []));
  }

  // Minimal keyed patch: keeps nodes that are already in place, appends pure
  // tails, and only falls back to replaceChildren for real reorders.
  function patchChildren(parent, next) {
    const cur = Array.from(parent.children || []);
    let i = 0;
    while (i < next.length && i < cur.length && cur[i] === next[i]) i++;
    if (i === cur.length && i === next.length) return;
    if (i === next.length && typeof parent.removeChild === "function") {
      for (let j = cur.length - 1; j >= i; j--) parent.removeChild(cur[j]);
      return;
    }
    if (i === cur.length) {
      parent.append(...next.slice(i));
      return;
    }
    parent.replaceChildren(...next);
  }

  function createMessengerController() {
    let mounted = false;
    let opts = null;
    let cancel = null;
    let openState = false;
    let pinned = false;
    let tier1Open = false;
    let sending = false;
    let fetchedOnce = false;
    let snapshot = {
      seq: 0,
      state: "laptop_ambient",
      posture: "laptop",
      phoneFaceDown: false,
      pendingApproval: null,
      messages: [],
      unreadClient: 0,
      unreadAgent: 0,
      tier1Summary: [],
      rotationMeta: { turns: 0, maxTurns: 20, summaryCap: 10 }
    };
    const localCardStatus = new Map();

    let popup = null, caret = null, statusDot = null, subtitleEl = null;
    let tier1Btn = null, tier1Dot = null, pinBtn = null, settingsBtn = null, closeBtn = null;
    let tier1Strip = null, tier1Metrics = null, tier1SummaryEl = null, tier1ResetBtn = null;
    let threadEl = null, typingEl = null, typingLabel = null;
    let inputEl = null, sendBtn = null;
    // WsInput instance owning inputEl (null in the plain-textarea fallback)
    // and per-key rendered thread nodes.
    let inputApi = null;
    const nodeCache = new Map();

    function docRef() {
      return opts?.document || (typeof document !== "undefined" ? document : null);
    }

    function fetchImpl() {
      return opts?.fetch || (typeof fetch === "function" ? fetch.bind(global) : null);
    }

    function buildDom(doc) {
      popup = doc.createElement("section");
      popup.id = "companion-messenger";
      popup.className = "ws-cm-popup is-closed";
      popup.setAttribute("role", "dialog");
      popup.setAttribute("aria-modal", "false");
      popup.setAttribute("aria-hidden", "true");
      popup.setAttribute("aria-label", t("js.companion.messenger.title", "Companion"));

      caret = doc.createElement("div");
      caret.className = "ws-cm-caret";
      caret.setAttribute("aria-hidden", "true");

      // Header
      const header = doc.createElement("header");
      header.className = "ws-cm-header";

      const avatarWrap = doc.createElement("div");
      avatarWrap.className = "ws-cm-avatar";
      avatarWrap.append(makeSvg(doc, ICONS.avatar));
      statusDot = doc.createElement("span");
      statusDot.className = "ws-cm-status-dot";
      statusDot.dataset.state = "laptop";
      avatarWrap.append(statusDot);

      const meta = doc.createElement("div");
      meta.className = "ws-cm-head-meta";
      const nameRow = doc.createElement("div");
      nameRow.className = "ws-cm-name-row";
      const nameEl = doc.createElement("strong");
      nameEl.className = "ws-cm-name";
      nameEl.textContent = t("js.companion.messenger.title", "Companion");
      const rolePill = doc.createElement("span");
      rolePill.className = "ws-cm-role-pill";
      rolePill.textContent = "Co-Lead";
      nameRow.append(nameEl, rolePill);
      subtitleEl = doc.createElement("div");
      subtitleEl.className = "ws-cm-subtitle";
      meta.append(nameRow, subtitleEl);

      const actions = doc.createElement("div");
      actions.className = "ws-cm-head-actions";

      tier1Btn = doc.createElement("button");
      tier1Btn.type = "button";
      tier1Btn.className = "ws-cm-ibtn";
      tier1Btn.dataset.action = "toggle-tier1";
      tier1Btn.setAttribute("aria-pressed", "false");
      tier1Btn.setAttribute("aria-expanded", "false");
      tier1Btn.title = t("js.companion.messenger.tier1_toggle", "Tier-1 summary");
      tier1Btn.setAttribute("aria-label", tier1Btn.title);
      tier1Btn.append(makeSvg(doc, ICONS.layers));
      tier1Dot = doc.createElement("span");
      tier1Dot.className = "ws-cm-ibtn-dot";
      tier1Dot.hidden = true;
      tier1Btn.append(tier1Dot);

      pinBtn = doc.createElement("button");
      pinBtn.type = "button";
      pinBtn.className = "ws-cm-ibtn";
      pinBtn.dataset.action = "toggle-pin";
      pinBtn.setAttribute("aria-pressed", "false");
      pinBtn.title = t("js.companion.messenger.pin", "Pin popup");
      pinBtn.setAttribute("aria-label", pinBtn.title);
      pinBtn.append(makeSvg(doc, ICONS.pin));

      settingsBtn = doc.createElement("button");
      settingsBtn.type = "button";
      settingsBtn.className = "ws-cm-ibtn";
      settingsBtn.dataset.action = "open-settings";
      settingsBtn.title = t("js.companion.settings", "Companion settings");
      settingsBtn.setAttribute("aria-label", settingsBtn.title);
      settingsBtn.append(makeSvg(doc, ICONS.gear));
      if (!opts?.openSettings) settingsBtn.hidden = true;

      closeBtn = doc.createElement("button");
      closeBtn.type = "button";
      closeBtn.className = "ws-cm-ibtn ws-cm-close";
      closeBtn.dataset.action = "close-messenger";
      closeBtn.title = t("js.companion.messenger.close", "Close messenger (Esc)");
      closeBtn.setAttribute("aria-label", closeBtn.title);
      closeBtn.append(makeSvg(doc, ICONS.close));

      actions.append(tier1Btn, pinBtn, settingsBtn, closeBtn);
      header.append(avatarWrap, meta, actions);

      // Tier-1 BoundedRotation Summary Strip
      tier1Strip = doc.createElement("div");
      tier1Strip.className = "ws-cm-tier1-strip";
      tier1Strip.hidden = true;
      const t1Head = doc.createElement("div");
      t1Head.className = "ws-cm-tier1-head";
      const t1Badge = doc.createElement("span");
      t1Badge.className = "ws-cm-tier1-badge";
      t1Badge.textContent = "Tier-1 · BoundedRotation";
      tier1Metrics = doc.createElement("span");
      tier1Metrics.className = "ws-cm-tier1-metrics";
      tier1ResetBtn = doc.createElement("button");
      tier1ResetBtn.type = "button";
      tier1ResetBtn.className = "ws-cm-tier1-reset";
      tier1ResetBtn.textContent = t("js.companion.messenger.tier1_reset", "Reset");
      t1Head.append(t1Badge, tier1Metrics, tier1ResetBtn);
      tier1SummaryEl = doc.createElement("div");
      tier1SummaryEl.className = "ws-cm-tier1-summary";
      tier1Strip.append(t1Head, tier1SummaryEl);

      // Scrollable Thread
      threadEl = doc.createElement("div");
      threadEl.className = "ws-cm-thread";
      threadEl.setAttribute("role", "log");
      threadEl.setAttribute("aria-live", "polite");

      // Typing / Choreography Indicator
      typingEl = doc.createElement("div");
      typingEl.className = "ws-cm-typing";
      typingEl.hidden = true;
      const dots = doc.createElement("span");
      dots.className = "ws-cm-typing-dots";
      dots.setAttribute("aria-hidden", "true");
      dots.append(doc.createElement("i"), doc.createElement("i"), doc.createElement("i"));
      typingLabel = doc.createElement("span");
      typingEl.append(dots, typingLabel);

      // Composer Footer
      const composer = doc.createElement("footer");
      composer.className = "ws-cm-composer";
      const well = doc.createElement("div");
      well.className = "ws-cm-input-well";
      inputEl = doc.createElement("textarea");
      inputEl.className = "ws-cm-input";
      inputEl.rows = 1;
      inputEl.placeholder = t("js.companion.messenger.placeholder", "Message… (Enter to send)");
      inputEl.setAttribute("aria-label", inputEl.placeholder);
      sendBtn = doc.createElement("button");
      sendBtn.type = "button";
      sendBtn.className = "ws-cm-send-btn";
      sendBtn.disabled = true;
      sendBtn.title = t("js.companion.messenger.send", "Send message");
      sendBtn.setAttribute("aria-label", sendBtn.title);
      sendBtn.append(makeSvg(doc, ICONS.send));
      well.append(inputEl, sendBtn);
      composer.append(well);

      popup.append(caret, header, tier1Strip, threadEl, typingEl, composer);
      doc.body.append(popup);
    }

    function syncInputSize() {
      if (!inputEl) return;
      // With WsInput the visible field is the capsule editor, sized by CSS
      // (min/max-height + overflow); only the plain textarea needs a measure.
      if (!inputApi) {
        inputEl.style.height = "auto";
        const nextH = clamp(inputEl.scrollHeight || 28, 28, 92);
        inputEl.style.height = nextH + "px";
      }
      if (sendBtn) sendBtn.disabled = sending || !inputEl.value.trim();
    }

    // Attaches the shared WsInput primitive (capsule editor + live markdown
    // via WsMdEditor, list continuation, IME composition guard) to the
    // composer textarea. global.WsInput is resolved here, at mount time, so
    // script order never matters; a missing WsInput leaves the plain textarea
    // with its own Enter handler (see mount()).
    function attachWsInput() {
      const WsInput = global.WsInput;
      if (!inputEl || !WsInput || typeof WsInput.attach !== "function") return null;
      try {
        return WsInput.attach(inputEl, {
          // Messenger chat convention: Enter sends, Shift+Enter adds a line.
          keymap: () => "enter-send",
          features: {
            markdown: true,
            capsules: true,
            mentions: false,
            topics: false,
            slash: false,
            imagePaste: false,
            longPaste: false,
            listContinuation: true,
            bangMode: false,
            autoGrow: true
          },
          onChange: () => { syncInputSize(); },
          onSubmit: () => { sendMessage(); }
        });
      } catch (_) {
        return null;
      }
    }

    function resetComposer() {
      if (inputApi && typeof inputApi.setValue === "function") inputApi.setValue("");
      else if (inputEl) inputEl.value = "";
    }

    function reposition() {
      if (!popup || !mounted) return;
      const anchorRect = typeof opts?.getAnchorRect === "function" ? opts.getAnchorRect() : null;
      const vw = global.innerWidth || 1280;
      const vh = global.innerHeight || 800;
      const rect = computePopupRect(anchorRect, { width: vw, height: vh }, { width: POPUP_DEFAULT_W, height: POPUP_DEFAULT_H });
      popup.style.left = rect.left + "px";
      popup.style.top = rect.top + "px";
      popup.style.width = rect.width + "px";
      popup.style.height = rect.height + "px";
      if (caret) {
        caret.dataset.side = rect.caretSide;
        caret.style.bottom = rect.caretBottom + "px";
      }
    }

    function renderConfirmCard(doc, card) {
      const status = localCardStatus.get(card.id) || card.status || "pending";
      const wrap = doc.createElement("div");
      wrap.className = "ws-cm-confirm-card";
      wrap.dataset.status = status;

      const head = doc.createElement("div");
      head.className = "ws-cm-confirm-head";
      head.append(makeSvg(doc, ICONS.shield));
      const badge = doc.createElement("span");
      badge.textContent = status === "approved"
        ? t("js.companion.messenger.confirm_approved", "Approved")
        : status === "cancelled"
          ? t("js.companion.messenger.confirm_cancelled", "Cancelled")
          : t("js.companion.messenger.confirm_badge", "1-Click Approval");
      head.append(badge);
      if (card.tool) {
        const tool = doc.createElement("span");
        tool.className = "ws-cm-confirm-tool";
        tool.textContent = card.tool;
        head.append(tool);
      }

      const body = doc.createElement("div");
      body.className = "ws-cm-confirm-body";
      const title = doc.createElement("p");
      title.className = "ws-cm-confirm-title";
      title.textContent = card.title || "";
      body.append(title);
      if (card.desc) {
        const desc = doc.createElement("p");
        desc.className = "ws-cm-confirm-desc";
        desc.textContent = card.desc;
        body.append(desc);
      }
      if (Array.isArray(card.targets) && card.targets.length) {
        const targets = doc.createElement("div");
        targets.className = "ws-cm-confirm-targets";
        for (const item of card.targets) {
          const chip = doc.createElement("span");
          chip.className = "ws-cm-target-chip";
          chip.textContent = String(item);
          targets.append(chip);
        }
        body.append(targets);
      }

      if (status === "pending") {
        const acts = doc.createElement("div");
        acts.className = "ws-cm-confirm-actions";
        const approve = doc.createElement("button");
        approve.type = "button";
        approve.className = "ws-cm-btn-approve";
        approve.textContent = t("js.companion.messenger.confirm_approve", "Approve & Run");
        approve.addEventListener("click", () => {
          localCardStatus.set(card.id, "approved");
          render();
        });
        const cancelBtn = doc.createElement("button");
        cancelBtn.type = "button";
        cancelBtn.className = "ws-cm-btn-cancel";
        cancelBtn.textContent = t("js.companion.messenger.confirm_cancel", "Cancel");
        cancelBtn.addEventListener("click", () => {
          localCardStatus.set(card.id, "cancelled");
          render();
        });
        acts.append(approve, cancelBtn);
        body.append(acts);
      } else {
        const receipt = doc.createElement("div");
        receipt.className = "ws-cm-confirm-receipt";
        receipt.textContent = card.receipt || t("js.companion.messenger.confirm_phase2", "Action execution arrives in Phase 2.");
        body.append(receipt);
      }

      wrap.append(head, body);
      return wrap;
    }

    function render() {
      if (!popup || !mounted) return;
      const doc = docRef();
      const st = statusForSnapshot(snapshot);
      if (statusDot) statusDot.dataset.state = st.dot;
      if (subtitleEl) subtitleEl.textContent = st.subtitle;

      const summaries = Array.isArray(snapshot.tier1Summary) ? snapshot.tier1Summary : [];
      if (tier1Dot) tier1Dot.hidden = summaries.length === 0;
      if (tier1Metrics) {
        const turns = snapshot.rotationMeta?.turns ?? 0;
        const maxTurns = snapshot.rotationMeta?.maxTurns ?? 20;
        tier1Metrics.textContent = t("js.companion.messenger.tier1_turns", "Turns %d/%d", turns, maxTurns);
      }
      if (tier1SummaryEl) {
        tier1SummaryEl.replaceChildren();
        if (summaries.length === 0) {
          const empty = doc.createElement("p");
          empty.className = "ws-cm-tier1-empty";
          empty.textContent = t("js.companion.messenger.tier1_empty", "No Tier-1 summary entries yet.");
          tier1SummaryEl.append(empty);
        } else {
          for (const line of summaries) {
            const p = doc.createElement("p");
            p.className = "ws-cm-tier1-line";
            p.textContent = "• " + String(line);
            tier1SummaryEl.append(p);
          }
        }
      }

      if (typingEl && typingLabel) {
        typingEl.hidden = !st.typing;
        typingLabel.textContent = st.typing;
      }

      if (threadEl) {
        const wasNearBottom = (threadEl.scrollHeight - threadEl.scrollTop - threadEl.clientHeight) < 48;
        const msgs = Array.isArray(snapshot.messages) ? snapshot.messages : [];
        const pendingApproval = snapshot.pendingApproval && snapshot.pendingApproval.requestId ? snapshot.pendingApproval : null;
        // Patch by key: each row (and the empty/echo blocks) is cached with a
        // signature of everything it displays. A typing/state-only snapshot
        // re-uses the same nodes, so unchanged bubbles are neither re-parsed
        // as Markdown nor re-inserted (which would replay the entry animation).
        const used = new Set();
        const keyed = (key, sig, build) => {
          used.add(key);
          const hit = nodeCache.get(key);
          if (hit && hit.sig === sig) return hit.node;
          const node = build();
          nodeCache.set(key, { sig, node });
          return node;
        };
        const nextNodes = [];
        if (msgs.length === 0 && !pendingApproval) {
          nextNodes.push(keyed("empty", "empty", () => {
            const empty = doc.createElement("div");
            empty.className = "ws-cm-empty";
            empty.textContent = t("js.companion.messenger.empty", "Send a quick note — it buzzes your companion's desk smartphone.");
            return empty;
          }));
        } else {
          let prevSender = "";
          msgs.forEach((msg, idx) => {
            const isUser = msg.sender === "user";
            const isCont = prevSender === msg.sender;
            prevSender = msg.sender;
            const card = msg.confirmCard && typeof msg.confirmCard === "object" ? msg.confirmCard : null;
            const sig = JSON.stringify([
              msg.sender, isCont, msg.text || "", msg.tone || "", msg.createdAt || 0,
              card ? [card, localCardStatus.get(card.id) || ""] : null
            ]);
            nextNodes.push(keyed("m:" + (msg.id || "#" + idx), sig, () => {
              const row = doc.createElement("div");
              row.className = "ws-cm-msg " + (isUser ? "user" : "ai") + (isCont ? " is-cont" : "");

              if (!isUser) {
                const av = doc.createElement("div");
                av.className = "ws-cm-msg-av" + (isCont ? " is-cont" : "");
                if (!isCont) av.append(makeSvg(doc, ICONS.avatar));
                row.append(av);
              }

              const col = doc.createElement("div");
              col.className = "ws-cm-msg-col";
              if (!isUser && !isCont) {
                const sender = doc.createElement("span");
                sender.className = "ws-cm-sender";
                sender.textContent = t("js.companion.messenger.title", "Companion");
                col.append(sender);
              }

              const line = doc.createElement("div");
              line.className = "ws-cm-bubble-line";
              const bubble = doc.createElement("div");
              bubble.className = "ws-cm-bubble ws-cm-md";
              renderMarkdownInto(doc, bubble, msg.text || "");
              const time = doc.createElement("time");
              time.className = "ws-cm-time";
              time.textContent = formatTime(msg.createdAt);
              line.append(bubble, time);
              col.append(line);

              const tm = !isUser ? toneMeta(msg.tone) : null;
              if (tm) {
                const pills = doc.createElement("div");
                pills.className = "ws-cm-meta-pills";
                const pill = doc.createElement("span");
                pill.className = "ws-cm-react-pill" + (tm.cls ? " " + tm.cls : "");
                pill.textContent = tm.label;
                pills.append(pill);
                col.append(pills);
              }
              if (card) col.append(renderConfirmCard(doc, card));
              row.append(col);
              return row;
            }));
          });
        }
        if (pendingApproval) {
          const echoSig = JSON.stringify([pendingApproval.requestId, pendingApproval.tool || "", pendingApproval.summary || ""]);
          nextNodes.push(keyed("echo", echoSig, () => {
          const echo = doc.createElement("div");
          echo.className = "ws-cm-pause-echo";
          echo.setAttribute("role", "status");
          echo.setAttribute("data-request-id", pendingApproval.requestId);

          const head = doc.createElement("div");
          head.className = "ws-cm-pause-head";
          head.append(makeSvg(doc, ICONS.shield));
          const title = doc.createElement("span");
          title.className = "ws-cm-pause-title";
          title.textContent = t("js.companion.messenger.approval_paused_title", "Waiting for 1-click approval");
          head.append(title);
          if (pendingApproval.tool) {
            const tool = doc.createElement("span");
            tool.className = "ws-cm-pause-tool";
            tool.textContent = pendingApproval.tool;
            head.append(tool);
          }
          echo.append(head);

          if (pendingApproval.summary) {
            const sum = doc.createElement("p");
            sum.className = "ws-cm-pause-summary";
            sum.textContent = pendingApproval.summary;
            echo.append(sum);
          }
          const hint = doc.createElement("p");
          hint.className = "ws-cm-pause-hint";
          hint.textContent = t("js.companion.messenger.approval_paused_hint", "Approve or deny in the top confirmation banner to continue.");
          echo.append(hint);
          return echo;
          }));
        }
        for (const key of [...nodeCache.keys()]) {
          if (!used.has(key)) nodeCache.delete(key);
        }
        patchChildren(threadEl, nextNodes);
        if (wasNearBottom || msgs.length <= 2 || Boolean(pendingApproval)) {
          threadEl.scrollTop = threadEl.scrollHeight;
        }
      }
      syncInputSize();
    }

    async function fetchThread() {
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = await f("/api/companion/phone/thread", { credentials: "same-origin" });
        if (!res || !res.ok) return;
        const data = await res.json();
        fetchedOnce = true;
        applySnapshot(data);
        opts?.onSnapshot?.(data);
      } catch (_) { /* best effort */ }
    }

    async function markRead() {
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = await f("/api/companion/phone/mark-read", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: "{}"
        });
        if (res && res.ok) {
          const data = await res.json();
          applySnapshot(data);
          opts?.onSnapshot?.(data);
        }
      } catch (_) { /* best effort */ }
    }

    async function sendMessage() {
      if (!inputEl || sending) return;
      const text = inputEl.value.trim();
      if (!text) return;
      const f = fetchImpl();
      if (!f) return;
      sending = true;
      syncInputSize();
      try {
        const res = await f("/api/companion/phone/send", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ text })
        });
        if (!res || !res.ok) {
          global.WsToast?.show?.(t("js.companion.messenger.send_failed", "Failed to send message."), { kind: "warn" });
          return;
        }
        const data = await res.json();
        resetComposer();
        // The server answers {message, thread}; `snapshot` is kept as a fallback.
        const next = data?.thread || data?.snapshot;
        if (next) {
          applySnapshot(next);
          opts?.onSnapshot?.(next);
        }
      } catch (_) {
        global.WsToast?.show?.(t("js.companion.messenger.send_failed", "Failed to send message."), { kind: "warn" });
      } finally {
        sending = false;
        syncInputSize();
        inputEl?.focus?.();
      }
    }

    async function resetSummary() {
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = await f("/api/companion/phone/reset-summary", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: "{}"
        });
        if (res && res.ok) {
          const data = await res.json();
          applySnapshot(data);
          opts?.onSnapshot?.(data);
        }
      } catch (_) { /* best effort */ }
    }

    function applySnapshot(next) {
      if (!next || typeof next !== "object") return;
      // SSE events and POST responses race: drop a snapshot older than the
      // one shown (same daemon epoch, lower seq).
      const newer = global.WsCompanion?.phoneSnapNewer;
      if (typeof newer === "function" && !newer(snapshot, next)) return;
      const pendingApproval = next.pendingApproval && typeof next.pendingApproval === "object" && next.pendingApproval.requestId
        ? next.pendingApproval
        : null;
      snapshot = {
        ...snapshot,
        ...next,
        pendingApproval,
        messages: Array.isArray(next.messages) ? next.messages : snapshot.messages,
        tier1Summary: Array.isArray(next.tier1Summary) ? next.tier1Summary : snapshot.tier1Summary,
        rotationMeta: next.rotationMeta && typeof next.rotationMeta === "object" ? next.rotationMeta : snapshot.rotationMeta
      };
      render();
      if (openState && (snapshot.unreadClient || 0) > 0) {
        markRead();
      }
    }

    function open() {
      if (!mounted || !popup) return false;
      openState = true;
      reposition();
      popup.classList.remove("is-closed");
      popup.setAttribute("aria-hidden", "false");
      opts?.onOpenChange?.(true);
      if (!fetchedOnce) fetchThread();
      if ((snapshot.unreadClient || 0) > 0) markRead();
      render();
      if (threadEl) threadEl.scrollTop = threadEl.scrollHeight;
      inputEl?.focus?.();
      return true;
    }

    function close() {
      if (!mounted || !popup || !openState) return false;
      openState = false;
      popup.classList.add("is-closed");
      popup.setAttribute("aria-hidden", "true");
      opts?.onOpenChange?.(false);
      return true;
    }

    function toggle() {
      return openState ? (close(), false) : open();
    }

    function mount(mountOptions) {
      if (mounted) dispose();
      opts = mountOptions || {};
      const doc = docRef();
      if (!doc || !doc.body) return;
      mounted = true;
      cancel = new AbortController();
      const sig = { signal: cancel.signal };
      buildDom(doc);

      tier1Btn.addEventListener("click", () => {
        tier1Open = !tier1Open;
        tier1Strip.hidden = !tier1Open;
        tier1Btn.setAttribute("aria-pressed", tier1Open ? "true" : "false");
        tier1Btn.setAttribute("aria-expanded", tier1Open ? "true" : "false");
      }, sig);

      pinBtn.addEventListener("click", () => {
        pinned = !pinned;
        pinBtn.setAttribute("aria-pressed", pinned ? "true" : "false");
      }, sig);

      settingsBtn.addEventListener("click", () => {
        if (!pinned) close();
        opts?.openSettings?.(settingsBtn);
      }, sig);

      closeBtn.addEventListener("click", () => close(), sig);
      tier1ResetBtn.addEventListener("click", () => resetSummary(), sig);
      sendBtn.addEventListener("click", () => sendMessage(), sig);

      // WsInput, when present, owns Enter (send) / Shift+Enter (newline), the
      // IME composition guard and list continuation; its onChange keeps the
      // send button state in sync. The plain textarea gets the same keys.
      inputApi = attachWsInput();
      if (!inputApi) {
        inputEl.addEventListener("input", syncInputSize, sig);
        inputEl.addEventListener("keydown", event => {
          if (event.key === "Enter" && !event.shiftKey && !event.isComposing && event.keyCode !== 229) {
            event.preventDefault();
            sendMessage();
          }
        }, sig);
      }

      doc.addEventListener("keydown", event => {
        if (event.key === "Escape" && openState && !pinned) {
          event.preventDefault();
          close();
          opts?.getChatButton?.()?.focus?.();
        }
      }, sig);

      doc.addEventListener("pointerdown", event => {
        if (!openState || pinned) return;
        const target = event.target;
        if (popup?.contains(target)) return;
        const btn = opts?.getChatButton?.();
        if (btn && (btn === target || btn.contains(target))) return;
        if (opts?.isToggleTarget?.(target, event)) return;
        close();
      }, sig);

      global.addEventListener?.("resize", reposition, sig);

      if (global.wsCompanionPhoneState && typeof global.wsCompanionPhoneState === "object") {
        applySnapshot(global.wsCompanionPhoneState);
      } else {
        render();
      }
      reposition();
    }

    function dispose() {
      cancel?.abort();
      cancel = null;
      try { inputApi?.destroy?.(); } catch (_) { /* best effort */ }
      inputApi = null;
      nodeCache.clear();
      popup?.remove?.();
      popup = caret = statusDot = subtitleEl = null;
      tier1Btn = tier1Dot = pinBtn = settingsBtn = closeBtn = null;
      tier1Strip = tier1Metrics = tier1SummaryEl = tier1ResetBtn = null;
      threadEl = typingEl = typingLabel = inputEl = sendBtn = null;
      openState = false;
      mounted = false;
    }

    return {
      mount,
      dispose,
      open,
      close,
      toggle,
      reposition,
      applySnapshot,
      isOpen() { return openState; },
      isPinned() { return pinned; },
      getState() { return { open: openState, pinned, tier1Open, snapshot }; },
      computePopupRect
    };
  }

  const controller = createMessengerController();
  global.WsCompanionMessenger = controller;
  if (global.WsCompanion) {
    global.WsCompanion.messenger = controller;
    global.WsCompanion.computePopupRect = computePopupRect;
  }
})(window);
