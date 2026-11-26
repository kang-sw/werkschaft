// User command editor parts: SVG icons, a zero-dependency bash tokenizer, the
// monospace overlay editor (transparent-text <textarea> over a highlighted
// <pre><code> backdrop with identical metrics), and the one-line collapsed
// preview. Tokens paint with the theme's shared --hl-* syntax colours.
  var UCMD_SVG_NS = "http://www.w3.org/2000/svg";
  var UCMD_CODE_LINE_PX = 16, UCMD_CODE_PAD_PX = 10, UCMD_CODE_MAX_PX = 140;
  var UCMD_BASH_KEYWORDS = Object.create(null);
  ("if then else elif fi for do done while until case esac in function select time " +
    "return exit export local set unset readonly declare typeset source break continue " +
    "shift trap eval exec").split(" ").forEach(function (w) { UCMD_BASH_KEYWORDS[w] = true; });
  // Keywords after which the next word still starts a command.
  var UCMD_BASH_LEADS = Object.create(null);
  "if then else elif do while until time !".split(" ").forEach(function (w) { UCMD_BASH_LEADS[w] = true; });

  function ucmdSvg(w, h, vb, parts) {
    var s = doc.createElementNS(UCMD_SVG_NS, "svg");
    s.setAttribute("width", String(w));
    s.setAttribute("height", String(h));
    s.setAttribute("viewBox", vb);
    s.setAttribute("fill", "none");
    s.setAttribute("aria-hidden", "true");
    parts.forEach(function (p) {
      var el = doc.createElementNS(UCMD_SVG_NS, p.t);
      Object.keys(p.a).forEach(function (k) { el.setAttribute(k, p.a[k]); });
      s.appendChild(el);
    });
    return s;
  }
  function ucmdStroke(d, w) {
    return { t: "path", a: { d: d, stroke: "currentColor", "stroke-width": w || "1.7", "stroke-linecap": "round", "stroke-linejoin": "round" } };
  }
  function ucmdTermIcon(sz) {
    return ucmdSvg(sz, sz, "0 0 16 16", [ucmdStroke("M3 5l3 3-3 3"), ucmdStroke("M8 11h5")]);
  }
  function ucmdPlayIcon() {
    return ucmdSvg(11, 11, "0 0 16 16", [{ t: "path", a: { d: "M4.5 3.3l8.5 4.7-8.5 4.7V3.3z", fill: "currentColor" } }]);
  }
  function ucmdAskIcon() {
    return ucmdSvg(12, 12, "0 0 16 16", [ucmdStroke("M4 12L12 4M6 4h6v6")]);
  }
  function ucmdSaveIcon() {
    return ucmdSvg(12, 12, "0 0 16 16", [
      ucmdStroke("M3.2 2.5h7.8l2.5 2.5v8.5a.8.8 0 0 1-.8.8H3.2a.8.8 0 0 1-.8-.8V3.3a.8.8 0 0 1 .8-.8z", "1.4"),
      ucmdStroke("M5 2.5v3.6h5V2.5M5 13.5V9.2h6v4.3", "1.4")
    ]);
  }
  function ucmdDelIcon() {
    return ucmdSvg(11, 11, "0 0 16 16", [ucmdStroke("M4 4l8 8M12 4l-8 8")]);
  }
  function ucmdPromptIcon() {
    return ucmdSvg(11, 11, "0 0 16 16", [
      ucmdStroke("M2.5 3.5h11a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H6.5l-3 2.5v-2.5h-1a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1z", "1.4")
    ]);
  }
  function ucmdRevertIcon() {
    return ucmdSvg(11, 11, "0 0 16 16", [ucmdStroke("M4 6.5A4.8 4.8 0 1 1 4.2 10.5M4 3v3.8h3.8", "1.6")]);
  }
  function ucmdChevIcon(up) {
    return ucmdSvg(10, 10, "0 0 16 16", [ucmdStroke(up ? "M4 10l4-4 4 4" : "M4 6l4 4 4-4")]);
  }

  // ucmdBashTokens splits text into [class, text] segments whose texts join
  // back to text exactly. Classes: k keyword, x command word, s string,
  // v variable/expansion, c comment, o operator/redirection, f flag, n number,
  // "" plain. A pragmatic single pass, not a shell parser.
  function ucmdBashTokens(text) {
    var out = [], src = String(text || ""), len = src.length, i = 0;
    var cmdPos = true, wordStart = true, assign = false, expectIn = 0;
    function push(cls, s) {
      if (!s) return;
      var last = out[out.length - 1];
      if (last && last[0] === cls) last[1] += s;
      else out.push([cls, s]);
    }
    function closeParen(from) {
      // from points just past "(", returns the index past the matching ")".
      var depth = 1, k = from;
      while (k < len && depth > 0) {
        var ch = src.charAt(k);
        if (ch === "\\") { k += 2; continue; }
        if (ch === "'") { var q = src.indexOf("'", k + 1); k = q < 0 ? len : q + 1; continue; }
        if (ch === "(") depth++;
        else if (ch === ")") depth--;
        k++;
      }
      return k;
    }
    // expansion returns the end index of a $-expansion starting at k, or k.
    function expansion(k) {
      var nx = src.charAt(k + 1);
      if (nx === "{") { var e = src.indexOf("}", k + 2); return e < 0 ? len : e + 1; }
      if (nx === "(") return closeParen(k + 2);
      var m = /^(?:[A-Za-z_][A-Za-z0-9_]*|[0-9?@#$!*\-])/.exec(src.slice(k + 1, k + 64));
      return m ? k + 1 + m[0].length : k;
    }
    function expanded() {
      if (cmdPos && !assign) cmdPos = false;
      wordStart = false;
    }
    while (i < len) {
      var ch = src.charAt(i), rest, m, end;
      if (ch === "\n") {
        push("", ch); i++;
        cmdPos = true; wordStart = true; assign = false; expectIn = 0;
        continue;
      }
      if (ch === " " || ch === "\t" || ch === "\r") {
        var ws = /^[ \t\r]+/.exec(src.slice(i))[0];
        push("", ws); i += ws.length;
        wordStart = true; assign = false;
        continue;
      }
      if (ch === "#" && wordStart) {
        end = src.indexOf("\n", i);
        if (end < 0) end = len;
        push("c", src.slice(i, end)); i = end;
        continue;
      }
      if (ch === "\\") {
        push("", src.slice(i, i + 2)); i += 2;
        wordStart = false;
        continue;
      }
      if (ch === "'") {
        end = src.indexOf("'", i + 1);
        end = end < 0 ? len : end + 1;
        push("s", src.slice(i, end)); i = end;
        expanded();
        continue;
      }
      if (ch === "\"") {
        var k = i + 1, from = i;
        while (k < len && src.charAt(k) !== "\"") {
          var c2 = src.charAt(k);
          if (c2 === "\\") { k += 2; continue; }
          if (c2 === "$" || c2 === "`") {
            var ee = c2 === "`" ? src.indexOf("`", k + 1) : expansion(k);
            if (c2 === "`") ee = ee < 0 ? len : ee + 1;
            if (ee > k) {
              push("s", src.slice(from, k));
              push("v", src.slice(k, ee));
              k = from = ee;
              continue;
            }
          }
          k++;
        }
        k = Math.min(len, k + 1);
        push("s", src.slice(from, k)); i = k;
        expanded();
        continue;
      }
      if (ch === "$") {
        if (src.charAt(i + 1) === "'") {
          var q2 = i + 2;
          while (q2 < len && src.charAt(q2) !== "'") q2 += src.charAt(q2) === "\\" ? 2 : 1;
          q2 = Math.min(len, q2 + 1);
          push("s", src.slice(i, q2)); i = q2;
          expanded();
          continue;
        }
        end = expansion(i);
        if (end > i) {
          push("v", src.slice(i, end)); i = end;
          expanded();
          continue;
        }
      }
      if (ch === "`") {
        end = src.indexOf("`", i + 1);
        end = end < 0 ? len : end + 1;
        push("v", src.slice(i, end)); i = end;
        expanded();
        continue;
      }
      rest = src.slice(i, i + 8);
      m = (wordStart ? /^(?:[0-9]*(?:>>|>&|<&|<<<|<<|>\||>|<)(?:[0-9]+-?|-)?|&>>?)/ : /^(?:>>|>&|<&|<<<|<<|>\||>|<|&>>?)(?:[0-9]+-?|-)?/).exec(rest);
      if (m && /[<>]/.test(m[0])) {
        push("o", m[0]); i += m[0].length;
        wordStart = true; assign = false;
        continue;
      }
      m = /^(?:&&|\|\||;;|\|&|[|;&()])/.exec(rest);
      if (m) {
        push("o", m[0]); i += m[0].length;
        cmdPos = m[0] !== ")"; wordStart = true; assign = false; expectIn = 0;
        continue;
      }
      m = /^[^\s|&;()<>'"`$\\]+/.exec(src.slice(i));
      var word = m ? m[0] : ch;
      i += word.length;
      if (!wordStart) { push("", word); continue; }
      wordStart = false;
      if (cmdPos && /^[A-Za-z_][A-Za-z0-9_]*\+?=/.test(word)) {
        var eq = word.indexOf("=");
        push("v", word.slice(0, eq));
        push("o", "=");
        push("", word.slice(eq + 1));
        assign = true;
        continue;
      }
      if (expectIn === 2 && word === "in") { push("k", word); expectIn = 0; continue; }
      if (expectIn === 1) { push("", word); expectIn = 2; continue; }
      if (cmdPos) {
        if (UCMD_BASH_KEYWORDS[word] || word === "!" || word === "{" || word === "}" || word === "[[" || word === "]]") {
          push(UCMD_BASH_KEYWORDS[word] ? "k" : "o", word);
          if (word === "for" || word === "case" || word === "select") { expectIn = 1; cmdPos = false; }
          else cmdPos = !!UCMD_BASH_LEADS[word] || word === "{" || word === "!";
          continue;
        }
        push("x", word);
        cmdPos = false;
        continue;
      }
      if (word.charAt(0) === "-" && word.length > 1 && !/^-[0-9.]+$/.test(word)) {
        var feq = word.indexOf("=");
        if (feq > 0) { push("f", word.slice(0, feq)); push("", word.slice(feq)); }
        else push("f", word);
        continue;
      }
      if (/^-?[0-9]+(?:\.[0-9]+)?$/.test(word)) { push("n", word); continue; }
      if (word === "]" || word === "]]") { push("o", word); continue; }
      push("", word);
    }
    return out;
  }

  function ucmdText(s) {
    return typeof doc.createTextNode === "function" ? doc.createTextNode(s) : (function () {
      var sp = doc.createElement("span");
      sp.textContent = s;
      return sp;
    })();
  }
  // ucmdPaintTokens replaces el's children with highlighted spans for text.
  function ucmdPaintTokens(el, segs) {
    if (typeof el.replaceChildren === "function") el.replaceChildren();
    else el.textContent = "";
    for (var i = 0; i < segs.length; i++) {
      var seg = segs[i];
      if (!seg[0]) { el.appendChild(ucmdText(seg[1])); continue; }
      var sp = doc.createElement("span");
      sp.className = "ucmd-hl-" + seg[0];
      sp.textContent = seg[1];
      el.appendChild(sp);
    }
  }

  // ucmdPreviewSegments renders the collapsed one-line summary: non-empty
  // trimmed lines, each tokenized on its own and joined with a ↵ separator.
  function ucmdPreviewSegments(text) {
    var lines = String(text || "").split("\n").map(function (l) { return l.trim(); }).filter(Boolean);
    var out = [];
    lines.forEach(function (l, idx) {
      if (idx) out.push(["sep", " ↵ "]);
      out.push.apply(out, ucmdBashTokens(l));
    });
    return out;
  }
  function ucmdLineCount(text) {
    return String(text || "").split("\n").length;
  }

  // ucmdCodeEditor builds the overlay editor: the <textarea> stays the native
  // input (undo, IME, selection, clipboard, caret) with transparent ink, and a
  // positioned <pre><code> behind it paints the same text highlighted. Both
  // share every metric in CSS; scroll is mirrored by a transform on <code>,
  // which has no scroll-range clamp, so the last line never drifts.
  function ucmdCodeEditor(ta) {
    var wrap = doc.createElement("div");
    wrap.className = "ucmd-code";
    var pre = doc.createElement("pre");
    pre.className = "ucmd-code-hl";
    pre.setAttribute("aria-hidden", "true");
    var code = doc.createElement("code");
    pre.appendChild(code);
    ta.classList.add("ucmd-code-ta");
    ta.spellcheck = false;
    ta.autocomplete = "off";
    ta.setAttribute("wrap", "off");
    ta.setAttribute("autocapitalize", "off");
    wrap.appendChild(pre);
    wrap.appendChild(ta);
    var ed = { wrap: wrap, ta: ta, pre: pre, code: code, painted: null, rows: 0 };
    ta.addEventListener("scroll", function () { ucmdCodeScroll(ed); });
    return ed;
  }
  // ucmdCodePaint repaints only when the text changed and sizes the field by
  // its line count (no layout read): 16px rows, capped at 140px, then scroll.
  function ucmdCodePaint(ed) {
    var text = ed.ta.value;
    if (ed.painted !== text) {
      ed.painted = text;
      // A trailing newline adds a row the <pre> would otherwise collapse.
      var segs = ucmdBashTokens(text);
      if (text.charAt(text.length - 1) === "\n") segs.push(["", " "]);
      ucmdPaintTokens(ed.code, segs);
    }
    var rows = Math.max(2, ucmdLineCount(text));
    if (rows !== ed.rows) {
      ed.rows = rows;
      ed.ta.style.setProperty("--ucmd-rows", String(rows));
    }
  }
  function ucmdCodeScroll(ed) {
    var x = ed.ta.scrollLeft || 0, y = ed.ta.scrollTop || 0;
    ed.code.style.transform = x || y ? "translate(" + (-x) + "px," + (-y) + "px)" : "";
  }
