(function (global) {
  "use strict";

  // ==========================================================================
  // Safe Markdown + badge rendering for Companion Messenger bubbles.
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

  const SVG_NS = "http://www.w3.org/2000/svg";

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
    close: [{ tag: "path", attrs: { d: "M6 6l12 12M18 6 6 18" } }],
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
    ],
    external: [
      { tag: "path", attrs: { d: "M15 3h6v6" } },
      { tag: "path", attrs: { d: "M10 14 21 3" } },
      { tag: "path", attrs: { d: "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" } }
    ]
  };

  function formatTime(ms) {
    if (!Number.isFinite(ms) || ms <= 0) return "";
    try {
      return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    } catch (_) {
      return "";
    }
  }

  function statusForSnapshot(snap, t) {
    const st = snap?.state || "laptop_ambient";
    const unreadAgent = Number(snap?.unreadAgent) || 0;
    if (snap?.pendingApproval && snap.pendingApproval.requestId) {
      return { dot: "approval", subtitle: t("js.companion.messenger.status.approval_paused", "Paused — waiting for 1-click approval"), typing: "" };
    }
    if (st === "phone_active") {
      return { dot: "phone", subtitle: t("js.companion.messenger.status.phone", "Checking smartphone with one hand"), typing: t("js.companion.messenger.typing.phone", "Checking smartphone…") };
    }
    if (st === "laptop_work") {
      return { dot: "work", subtitle: t("js.companion.messenger.status.work", "Handling your request on laptop"), typing: t("js.companion.messenger.typing.work", "Working on laptop…") };
    }
    if (snap?.unavailable) {
      return { dot: "unavailable", subtitle: t("js.companion.messenger.status.unavailable", "Companion is temporarily unavailable — send a message to retry"), typing: "" };
    }
    if (st === "laptop_sleepy") {
      return { dot: "sleepy", subtitle: t("js.companion.messenger.status.sleepy", "Dozing off… knock the glass to wake up"), typing: "" };
    }
    if (unreadAgent > 0) {
      return { dot: "buzz", subtitle: t("js.companion.messenger.status.buzz", "Smartphone buzz! Waiting to check"), typing: t("js.companion.messenger.typing.buzz", "Smartphone buzzing…") };
    }
    if (snap?.phoneFaceDown) {
      return { dot: "facedown", subtitle: t("js.companion.messenger.status.facedown", "Bleeh! Phone placed face-down"), typing: "" };
    }
    return { dot: "laptop", subtitle: t("js.companion.messenger.status.laptop", "Working on laptop · send a message to ping phone"), typing: "" };
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

  function renderConfirmCard(doc, card, localCardStatus, t, onChange) {
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
      approve.addEventListener("click", () => { localCardStatus.set(card.id, "approved"); onChange(); });
      const cancelBtn = doc.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.className = "ws-cm-btn-cancel";
      cancelBtn.textContent = t("js.companion.messenger.confirm_cancel", "Cancel");
      cancelBtn.addEventListener("click", () => { localCardStatus.set(card.id, "cancelled"); onChange(); });
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

  // Replaces container's content with the rendered Markdown of text.
  function renderMarkdownInto(doc, container, text) {
    const frag = doc.createElement("div");
    renderBlocks(doc, frag, String(text == null ? "" : text), 0);
    container.replaceChildren(...(frag.children || []));
  }

  const POPUP_DEFAULT_W = 360;
  const POPUP_DEFAULT_H = 540;
  const VIEWPORT_MARGIN = 12;
  const ANCHOR_GAP = 12;

  function clamp(v, lo, hi) {
    return Math.min(Math.max(v, lo), hi);
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
      left: Math.round(left), top: Math.round(top),
      width: Math.round(width), height: Math.round(height),
      caretSide, caretBottom: Math.round(caretBottom)
    };
  }

  const api = {
    renderMarkdownInto, renderBlocks, renderInline, safeHref, parseMailboxAt,
    ICONS, makeSvg, formatTime, statusForSnapshot, toneMeta, patchChildren, renderConfirmCard, computePopupRect
  };
  global.WsCompanionMessengerMd = api;
  if (global.WsCompanion) {
    global.WsCompanion.messengerMd = api;
  }
})(window);
