/* Pure full-spec CommonMark/GFM line and inline tokenizer for live editors.
 * Side-effect free: returns exact UTF-16 offset tokens covering every line
 * without rewriting or dropping any source characters. */
(function () {
  "use strict";

  var ESCAPABLE = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~";

  function isAsciiPunct(ch) {
    return ch.length === 1 && ESCAPABLE.indexOf(ch) >= 0;
  }

  function isWordChar(ch) {
    return /[A-Za-z0-9_]/.test(ch);
  }

  function isWhitespace(ch) {
    return ch === " " || ch === "\t" || ch === "\r" || ch === "\n";
  }

  function normalizeRefLabel(s) {
    return String(s || "").trim().replace(/[ \t\r\n]+/g, " ").toLowerCase();
  }

  function countIndentCols(str) {
    var cols = 0;
    for (var i = 0; i < str.length; i++) {
      cols += str.charAt(i) === "\t" ? 4 - (cols % 4) : 1;
    }
    return cols;
  }

  function hasUnescapedPipe(s) {
    for (var i = 0; i < s.length; i++) {
      var ch = s.charAt(i);
      if (ch === "\\" && i + 1 < s.length) { i++; continue; }
      if (ch === "`") {
        var ticks = 1;
        while (i + ticks < s.length && s.charAt(i + ticks) === "`") ticks++;
        var close = findClosingBackticks(s, i + ticks, s.length, ticks);
        i = close >= 0 ? close + ticks - 1 : i + ticks - 1;
        continue;
      }
      if (ch === "|") return true;
    }
    return false;
  }

  function splitTablePipes(s) {
    var pipes = [];
    for (var i = 0; i < s.length; i++) {
      var ch = s.charAt(i);
      if (ch === "\\" && i + 1 < s.length) { i++; continue; }
      if (ch === "`") {
        var ticks = 1;
        while (i + ticks < s.length && s.charAt(i + ticks) === "`") ticks++;
        var close = findClosingBackticks(s, i + ticks, s.length, ticks);
        i = close >= 0 ? close + ticks - 1 : i + ticks - 1;
        continue;
      }
      if (ch === "|") pipes.push(i);
    }
    return pipes;
  }

  function tableCellRanges(s) {
    var trimmedStart = 0, trimmedEnd = s.length;
    while (trimmedStart < trimmedEnd && (s.charAt(trimmedStart) === " " || s.charAt(trimmedStart) === "\t")) trimmedStart++;
    while (trimmedEnd > trimmedStart && (s.charAt(trimmedEnd - 1) === " " || s.charAt(trimmedEnd - 1) === "\t")) trimmedEnd--;
    var pipes = splitTablePipes(s);
    if (!pipes.length) return { pipes: [], cells: [] };
    var startIdx = 0, endIdx = pipes.length;
    var cellStart = trimmedStart;
    if (pipes[0] === trimmedStart) {
      cellStart = pipes[0] + 1;
      startIdx = 1;
    }
    var hasTrailingPipe = pipes[pipes.length - 1] === trimmedEnd - 1 && (pipes.length > 1 || pipes[0] !== trimmedStart);
    if (hasTrailingPipe) endIdx = pipes.length - 1;
    var cells = [];
    var cur = cellStart;
    for (var k = startIdx; k < endIdx; k++) {
      cells.push([cur, pipes[k]]);
      cur = pipes[k] + 1;
    }
    if (!hasTrailingPipe) {
      cells.push([cur, trimmedEnd]);
    } else if (endIdx < pipes.length) {
      cells.push([cur, pipes[endIdx]]);
    }
    return { pipes: pipes, cells: cells };
  }

  function isTableDelimRow(s) {
    if (!hasUnescapedPipe(s)) return false;
    var info = tableCellRanges(s);
    if (!info.cells.length) return false;
    for (var i = 0; i < info.cells.length; i++) {
      var cell = s.slice(info.cells[i][0], info.cells[i][1]).trim();
      if (!/^:?-{1,}:?$/.test(cell)) return false;
    }
    return true;
  }

  function isHorizontalRule(s) {
    return /^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/.test(s);
  }

  function isSetextUnderline(s) {
    var m = /^ {0,3}(=+|-+)[ \t]*$/.exec(s);
    return m ? (m[1].charAt(0) === "=" ? 1 : 2) : 0;
  }

  function findClosingBackticks(text, from, end, count) {
    var k = from;
    while (k < end) {
      if (text.charCodeAt(k) === 96) {
        var run = 1;
        while (k + run < end && text.charCodeAt(k + run) === 96) run++;
        if (run === count) return k;
        k += run;
      } else {
        k++;
      }
    }
    return -1;
  }

  function capsuleAt(capsules, offset) {
    for (var i = 0; i < capsules.length; i++) {
      var c = capsules[i];
      if (offset >= c.start && offset < c.end) return c;
      if (c.start > offset) break;
    }
    return null;
  }

  function overlapsRangeList(ranges, start, end) {
    for (var i = 0; i < ranges.length; i++) {
      if (start < ranges[i][1] && end > ranges[i][0]) return true;
    }
    return false;
  }

  function parseLinkDest(text, from, end) {
    // Expects text[from] === "("; returns { urlStart, urlEnd, closePos } or null.
    if (from >= end || text.charAt(from) !== "(") return null;
    var p = from + 1;
    while (p < end && (text.charAt(p) === " " || text.charAt(p) === "\t")) p++;
    if (p >= end) return null;
    var urlStart = p, urlEnd = p;
    if (text.charAt(p) === "<") {
      urlStart = p + 1;
      p++;
      while (p < end && text.charAt(p) !== ">" && text.charAt(p) !== "\n" && text.charAt(p) !== "<") {
        if (text.charAt(p) === "\\" && p + 1 < end) { p += 2; continue; }
        p++;
      }
      if (p >= end || text.charAt(p) !== ">") return null;
      urlEnd = p;
      p++;
    } else {
      var parens = 0;
      while (p < end) {
        var ch = text.charAt(p);
        if (ch === "\\" && p + 1 < end && isAsciiPunct(text.charAt(p + 1))) { p += 2; continue; }
        if (ch === "(") { parens++; p++; continue; }
        if (ch === ")") {
          if (parens === 0) break;
          parens--; p++; continue;
        }
        if (isWhitespace(ch)) break;
        p++;
      }
      urlEnd = p;
      if (urlEnd === urlStart && text.charAt(p) !== ")") return null;
    }
    var afterUrl = p;
    while (p < end && (text.charAt(p) === " " || text.charAt(p) === "\t")) p++;
    // Optional link title "..." / '...' / (...)
    if (p > afterUrl && p < end && (text.charAt(p) === '"' || text.charAt(p) === "'" || text.charAt(p) === "(")) {
      var openQ = text.charAt(p), closeQ = openQ === "(" ? ")" : openQ;
      p++;
      while (p < end && text.charAt(p) !== closeQ) {
        if (text.charAt(p) === "\\" && p + 1 < end) { p += 2; continue; }
        p++;
      }
      if (p >= end || text.charAt(p) !== closeQ) return null;
      p++;
      while (p < end && (text.charAt(p) === " " || text.charAt(p) === "\t")) p++;
    }
    if (p >= end || text.charAt(p) !== ")") return null;
    return { urlStart: urlStart, urlEnd: urlEnd, closePos: p };
  }

  function findClosingBracket(text, from, end, capsules) {
    var depth = 0, i = from;
    while (i < end) {
      var cap = capsuleAt(capsules, i);
      if (cap) { i = cap.end; continue; }
      var ch = text.charAt(i);
      if (ch === "\\" && i + 1 < end && isAsciiPunct(text.charAt(i + 1))) { i += 2; continue; }
      if (ch === "`") {
        var ticks = 1;
        while (i + ticks < end && text.charCodeAt(i + ticks) === 96) ticks++;
        var close = findClosingBackticks(text, i + ticks, end, ticks);
        i = close >= 0 ? close + ticks : i + ticks;
        continue;
      }
      if (ch === "[") { depth++; i++; continue; }
      if (ch === "]") {
        if (depth === 0) return i;
        depth--; i++; continue;
      }
      i++;
    }
    return -1;
  }

  function findClosingDelim(text, from, end, delim, wordBound, capsules) {
    var dlen = delim.length, k = from;
    while (k + dlen <= end) {
      var cap = capsuleAt(capsules, k);
      if (cap) { k = cap.end; continue; }
      var ch = text.charAt(k);
      if (ch === "\\" && k + 1 < end && isAsciiPunct(text.charAt(k + 1))) { k += 2; continue; }
      if (ch === "`") {
        var run = 1;
        while (k + run < end && text.charCodeAt(k + run) === 96) run++;
        var closeCode = findClosingBackticks(text, k + run, end, run);
        k = closeCode >= 0 ? closeCode + run : k + run;
        continue;
      }
      if (text.slice(k, k + dlen) === delim) {
        if (dlen === 1 && k + 1 < end && text.charAt(k + 1) === delim) {
          k += 2;
          continue;
        }
        if (dlen === 2 && k + 2 < end && text.charAt(k + 2) === delim.charAt(0)) {
          // For ** inside ***foo***, check if preceded by non-space; allow closing at the end of a 3-run
          // only when not preceded by whitespace.
          if (k > from && !/[ \t]/.test(text.charAt(k - 1))) {
            if (!wordBound || k + dlen >= end || !isWordChar(text.charAt(k + dlen))) return k;
          }
          k++;
          continue;
        }
        if (k > from && !/[ \t]/.test(text.charAt(k - 1))) {
          if (!wordBound || k + dlen >= end || !isWordChar(text.charAt(k + dlen))) return k;
        }
      }
      k++;
    }
    return -1;
  }

  function flattenTokens(lineStart, lineEnd, annotations) {
    if (lineEnd <= lineStart) return [];
    if (!annotations.length) return [{ start: lineStart, end: lineEnd, cls: "" }];
    var pts = [lineStart, lineEnd];
    for (var i = 0; i < annotations.length; i++) {
      var a = annotations[i];
      var s = Math.max(lineStart, a.start), e = Math.min(lineEnd, a.end);
      if (e > s) { pts.push(s); pts.push(e); }
    }
    pts.sort(function (x, y) { return x - y; });
    var uniq = [pts[0]];
    for (var p = 1; p < pts.length; p++) {
      if (pts[p] !== pts[p - 1]) uniq.push(pts[p]);
    }
    var out = [];
    for (var u = 0; u < uniq.length - 1; u++) {
      var segS = uniq[u], segE = uniq[u + 1];
      var seen = {}, classes = [];
      for (var j = 0; j < annotations.length; j++) {
        var an = annotations[j];
        if (an.start <= segS && an.end >= segE) {
          var parts = an.cls.split(" ");
          for (var c = 0; c < parts.length; c++) {
            var name = parts[c];
            if (name && !seen[name]) { seen[name] = true; classes.push(name); }
          }
        }
      }
      var cls = classes.join(" ");
      if (out.length && out[out.length - 1].cls === cls && out[out.length - 1].end === segS) {
        out[out.length - 1].end = segE;
      } else {
        out.push({ start: segS, end: segE, cls: cls });
      }
    }
    return out;
  }

  function parseDocument(text, mentionList, capsuleRanges) {
    if (typeof text !== "string" || !text.length) return { lines: [], refDefs: {} };
    var capsules = Array.isArray(capsuleRanges)
      ? capsuleRanges.filter(function (c) { return c && Number.isInteger(c.start) && Number.isInteger(c.end) && c.end > c.start; })
          .slice().sort(function (a, b) { return a.start - b.start; })
      : [];

    // 1. Split raw lines with exact UTF-16 offsets.
    var raw = [];
    var pos = 0, len = text.length;
    while (pos <= len) {
      var nl = text.indexOf("\n", pos);
      var lineEnd = nl < 0 ? len : nl;
      raw.push({
        start: pos,
        end: lineEnd,
        hasNewline: nl >= 0,
        text: text.slice(pos, lineEnd),
        kind: "para",
        blockClasses: [],
        qDepth: 0,
        indent: 0,
        annotations: [],
        inlineRanges: []
      });
      if (nl < 0) break;
      pos = nl + 1;
    }

    var refDefs = {};
    var codeRanges = [];
    var inFence = false, fenceChar = "", fenceLen = 0;
    var prevKind = "blank";

    function addAnn(lineRec, s, e, cls) {
      if (e > s) lineRec.annotations.push({ start: s, end: e, cls: cls });
    }

    // 2. Pass 1: Block structure classification.
    for (var i = 0; i < raw.length; i++) {
      var lr = raw[i];
      var line = lr.text;
      var lineStart = lr.start;

      // Blank line
      if (/^[ \t]*$/.test(line)) {
        if (inFence) {
          lr.kind = "fence-body";
          lr.blockClasses.push("wsmd-fence-body");
          codeRanges.push([lr.start, lr.end]);
        } else {
          lr.kind = "blank";
          prevKind = "blank";
        }
        continue;
      }

      // Fenced code blocks (``` or ~~~)
      var fenceM = /^( {0,3})(`{3,}|~{3,})(.*)$/.exec(line);
      if (fenceM) {
        var fch = fenceM[2].charAt(0), flen = fenceM[2].length, finfo = fenceM[3];
        if (!inFence && (fch === "~" || finfo.indexOf("`") < 0)) {
          inFence = true; fenceChar = fch; fenceLen = flen;
          lr.kind = "fence-open";
          lr.blockClasses.push("wsmd-fence-open");
          var fStart = lineStart + fenceM[1].length;
          addAnn(lr, fStart, fStart + flen, "wsmd-marker");
          if (finfo.trim().length) {
            var langLead = /^[ \t]*/.exec(finfo)[0].length;
            addAnn(lr, fStart + flen + langLead, lr.end, "wsmd-code-lang");
          }
          codeRanges.push([lr.start, lr.end]);
          prevKind = "other";
          continue;
        }
        if (inFence && fch === fenceChar && flen >= fenceLen && /^[ \t]*$/.test(finfo)) {
          inFence = false; fenceChar = ""; fenceLen = 0;
          lr.kind = "fence-close";
          lr.blockClasses.push("wsmd-fence-close");
          addAnn(lr, lineStart + fenceM[1].length, lr.end, "wsmd-marker");
          codeRanges.push([lr.start, lr.end]);
          prevKind = "other";
          continue;
        }
      }
      if (inFence) {
        lr.kind = "fence-body";
        lr.blockClasses.push("wsmd-fence-body");
        codeRanges.push([lr.start, lr.end]);
        continue;
      }

      // 4-space / tab indented code block (when not interrupting paragraph or list)
      if ((/^ {4}/.test(line) || /^\t/.test(line)) && prevKind !== "para" && prevKind !== "list") {
        lr.kind = "indented-code";
        lr.blockClasses.push("wsmd-code-block");
        codeRanges.push([lr.start, lr.end]);
        prevKind = "indented-code";
        continue;
      }

      // Setext heading underline (=== or --- immediately after paragraph line(s))
      var setextLvl = isSetextUnderline(line);
      if (setextLvl && i > 0 && raw[i - 1].kind === "para" && raw[i - 1].qDepth === 0) {
        var hCls = "wsmd-h" + setextLvl;
        var back = i - 1;
        while (back >= 0 && raw[back].kind === "para" && raw[back].qDepth === 0) {
          raw[back].kind = "setext-head";
          raw[back].blockClasses.push("wsmd-head", hCls);
          back--;
        }
        lr.kind = "setext-marker";
        lr.blockClasses.push("wsmd-setext-marker");
        var uLead = /^ */.exec(line)[0].length;
        addAnn(lr, lineStart + uLead, lr.end, "wsmd-marker wsmd-setext-line");
        prevKind = "other";
        continue;
      }

      // Reference link definition: [label]: url "optional title"
      var refM = /^( {0,3})\[([^\]\r\n]+)\](:[ \t]*)(\S+)(?:([ \t]+)(?:"[^"\r\n]*"|'[^'\r\n]*'|\([^)\r\n]*\)))?[ \t]*$/.exec(line);
      if (refM && !/^\[(?:Image|Text) #[1-9][0-9]{0,5}\]/.test(line.slice(refM[1].length)) && !capsuleAt(capsules, lineStart + refM[1].length)) {
        var rLabel = normalizeRefLabel(refM[2]);
        if (rLabel) {
          if (!Object.prototype.hasOwnProperty.call(refDefs, rLabel)) refDefs[rLabel] = true;
          lr.kind = "refdef";
          lr.blockClasses.push("wsmd-refdef");
          var p0 = lineStart + refM[1].length;
          addAnn(lr, p0, p0 + 1, "wsmd-marker");
          addAnn(lr, p0 + 1, p0 + 1 + refM[2].length, "wsmd-ref-label");
          var colonPos = p0 + 1 + refM[2].length;
          addAnn(lr, colonPos, colonPos + 1 + refM[3].length, "wsmd-marker");
          var urlPos = colonPos + 1 + refM[3].length;
          addAnn(lr, urlPos, urlPos + refM[4].length, "wsmd-link-url");
          if (refM[5]) {
            var titlePos = urlPos + refM[4].length + refM[5].length;
            addAnn(lr, titlePos, lr.end, "wsmd-ref-title");
          }
          prevKind = "other";
          continue;
        }
      }

      // Blockquote prefix(es): > or > >
      var p = lineStart;
      var rest = line;
      var qMatch = /^ {0,3}(?:>[ \t]?)+/.exec(rest);
      if (qMatch) {
        var qStr = qMatch[0];
        var qDepth = 0;
        var qi = 0;
        while (qi < qStr.length) {
          if (qStr.charAt(qi) === ">") {
            qDepth++;
            var qEnd = qi + 1;
            if (qEnd < qStr.length && (qStr.charAt(qEnd) === " " || qStr.charAt(qEnd) === "\t")) qEnd++;
            addAnn(lr, lineStart + qi, lineStart + qEnd, "wsmd-marker wsmd-qbar wsmd-qbar-" + Math.min(qDepth, 4));
            qi = qEnd;
          } else {
            qi++;
          }
        }
        lr.qDepth = qDepth;
        lr.blockClasses.push("wsmd-quote");
        p += qStr.length;
        rest = text.slice(p, lr.end);
      }

      // GFM Table (only at top level, not inside blockquote)
      if (lr.qDepth === 0 && hasUnescapedPipe(rest) && i + 1 < raw.length && isTableDelimRow(raw[i + 1].text)) {
        var headInfo = tableCellRanges(rest);
        var delimInfo = tableCellRanges(raw[i + 1].text);
        if (headInfo.cells.length > 0 && headInfo.cells.length === delimInfo.cells.length) {
          // Header row
          lr.kind = "table-head";
          lr.blockClasses.push("wsmd-table-head");
          for (var hp = 0; hp < headInfo.pipes.length; hp++) {
            addAnn(lr, lineStart + headInfo.pipes[hp], lineStart + headInfo.pipes[hp] + 1, "wsmd-marker wsmd-table-pipe");
          }
          for (var hc = 0; hc < headInfo.cells.length; hc++) {
            lr.inlineRanges.push([lineStart + headInfo.cells[hc][0], lineStart + headInfo.cells[hc][1]]);
          }
          // Delimiter row
          var dr = raw[i + 1];
          dr.kind = "table-delim";
          dr.blockClasses.push("wsmd-table-delim");
          addAnn(dr, dr.start, dr.end, "wsmd-marker");
          i++;
          // Body rows
          while (i + 1 < raw.length) {
            var nextLine = raw[i + 1].text;
            if (/^[ \t]*$/.test(nextLine) || !hasUnescapedPipe(nextLine)) break;
            if (/^ {0,3}(?:`{3,}|~{3,}|#{1,6}(?:[ \t]|$)|>)/.test(nextLine) || isHorizontalRule(nextLine)) break;
            i++;
            var br = raw[i];
            br.kind = "table-row";
            br.blockClasses.push("wsmd-table-row");
            var bodyInfo = tableCellRanges(br.text);
            for (var bp = 0; bp < bodyInfo.pipes.length; bp++) {
              addAnn(br, br.start + bodyInfo.pipes[bp], br.start + bodyInfo.pipes[bp] + 1, "wsmd-marker wsmd-table-pipe");
            }
            for (var bc = 0; bc < bodyInfo.cells.length; bc++) {
              br.inlineRanges.push([br.start + bodyInfo.cells[bc][0], br.start + bodyInfo.cells[bc][1]]);
            }
          }
          prevKind = "other";
          continue;
        }
      }

      // ATX Heading (# to ######)
      var atxM = /^( {0,3})(#{1,6})(?:([ \t]+)(.*))?$/.exec(rest);
      if (atxM) {
        var level = atxM[2].length;
        var hStart = p + atxM[1].length;
        var hMarkerEnd = hStart + level + (atxM[3] ? atxM[3].length : 0);
        lr.kind = "atx-head";
        lr.blockClasses.push("wsmd-head", "wsmd-h" + level);
        addAnn(lr, hStart, hMarkerEnd, "wsmd-marker");
        var contentStart = hMarkerEnd;
        var contentEnd = lr.end;
        if (atxM[4]) {
          var closeM = /[ \t]+#+[ \t]*$/.exec(atxM[4]);
          if (closeM) {
            contentEnd = lr.end - closeM[0].length;
            addAnn(lr, contentEnd, lr.end, "wsmd-marker");
          }
        }
        if (contentEnd > contentStart) lr.inlineRanges.push([contentStart, contentEnd]);
        prevKind = "other";
        continue;
      }

      // Horizontal Rule (---, ***, ___)
      if (isHorizontalRule(rest)) {
        lr.kind = "hr";
        lr.blockClasses.push("wsmd-hr");
        var hrLead = /^[ \t]*/.exec(rest)[0].length;
        addAnn(lr, p + hrLead, lr.end, "wsmd-marker wsmd-hr-text");
        prevKind = "other";
        continue;
      }

      // Lists: task list, unordered list, ordered list
      var taskM = /^([ \t]*)([-*+])([ \t]+)(\[([ xX])\])(?=[ \t]|$)/.exec(rest);
      if (taskM) {
        lr.kind = "list";
        lr.blockClasses.push("wsmd-list");
        lr.indent = Math.min(6, Math.floor(countIndentCols(taskM[1]) / 2));
        var bulletPos = p + taskM[1].length;
        addAnn(lr, bulletPos, bulletPos + taskM[2].length, "wsmd-marker wsmd-list-marker");
        var boxPos = bulletPos + taskM[2].length + taskM[3].length;
        var checked = taskM[5] === "x" || taskM[5] === "X";
        addAnn(lr, boxPos, boxPos + taskM[4].length, checked ? "wsmd-task wsmd-task-done" : "wsmd-task wsmd-task-open");
        var bodyStart = boxPos + taskM[4].length;
        if (checked && lr.end > bodyStart) addAnn(lr, bodyStart, lr.end, "wsmd-task-checked");
        if (lr.end > bodyStart) lr.inlineRanges.push([bodyStart, lr.end]);
        prevKind = "list";
        continue;
      }

      var ulM = /^([ \t]*)([-*+])([ \t]+)/.exec(rest);
      var olM = !ulM ? /^([ \t]*)(\d{1,9}[.)])([ \t]+)/.exec(rest) : null;
      var listM = ulM || olM;
      if (listM) {
        lr.kind = "list";
        lr.blockClasses.push("wsmd-list");
        lr.indent = Math.min(6, Math.floor(countIndentCols(listM[1]) / 2));
        var mPos = p + listM[1].length;
        addAnn(lr, mPos, mPos + listM[2].length, "wsmd-marker wsmd-list-marker");
        var itemStart = mPos + listM[2].length + listM[3].length;
        if (lr.end > itemStart) lr.inlineRanges.push([itemStart, lr.end]);
        prevKind = "list";
        continue;
      }

      // Default paragraph line (possibly inside blockquote)
      lr.kind = lr.qDepth > 0 ? "quote-para" : "para";
      if (lr.end > p) lr.inlineRanges.push([p, lr.end]);
      prevKind = lr.qDepth > 0 ? "other" : "para";
    }

    // 3. Pass 2: Inline markdown scanning on every inline range.
    var inlineCodeRanges = [];

    function scanInline(lr, start, end, depth) {
      if (depth > 4 || start >= end) return;
      var idx = start;
      while (idx < end) {
        var cap = capsuleAt(capsules, idx);
        if (cap) { idx = Math.min(end, cap.end); continue; }

        var ch = text.charAt(idx);

        // 1. Backslash escape of ASCII punctuation
        if (ch === "\\" && idx + 1 < end && isAsciiPunct(text.charAt(idx + 1))) {
          addAnn(lr, idx, idx + 1, "wsmd-marker wsmd-escape-slash");
          idx += 2;
          continue;
        }

        // 2. Inline code span (multi-backtick)
        if (ch === "`") {
          var ticks = 1;
          while (idx + ticks < end && text.charCodeAt(idx + ticks) === 96) ticks++;
          var closeTick = findClosingBackticks(text, idx + ticks, end, ticks);
          if (closeTick >= 0) {
            addAnn(lr, idx, idx + ticks, "wsmd-marker wsmd-code-marker wsmd-code-open");
            addAnn(lr, closeTick, closeTick + ticks, "wsmd-marker wsmd-code-marker wsmd-code-close");
            addAnn(lr, idx, closeTick + ticks, "wsmd-code");
            inlineCodeRanges.push([idx, closeTick + ticks]);
            idx = closeTick + ticks;
            continue;
          }
          idx += ticks;
          continue;
        }

        // 3. Angle-bracket autolinks: <https://...>, <mailto:...>, <user@host>
        if (ch === "<") {
          var angleSub = text.slice(idx, end);
          var urlAuto = /^<((?:https?|ftp|mailto):[^\s<>]+)>/i.exec(angleSub);
          var emailAuto = !urlAuto ? /^<([A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+)>/.exec(angleSub) : null;
          var autoM = urlAuto || emailAuto;
          if (autoM) {
            addAnn(lr, idx, idx + 1, "wsmd-marker");
            addAnn(lr, idx + 1, idx + 1 + autoM[1].length, "wsmd-link wsmd-autolink");
            addAnn(lr, idx + autoM[0].length - 1, idx + autoM[0].length, "wsmd-marker");
            idx += autoM[0].length;
            continue;
          }
        }

        // 4. Image syntax: ![alt](url) or ![alt][ref]
        if (ch === "!" && idx + 1 < end && text.charAt(idx + 1) === "[") {
          var imgClose = findClosingBracket(text, idx + 2, end, capsules);
          if (imgClose >= idx + 2) {
            var imgDest = parseLinkDest(text, imgClose + 1, end);
            if (imgDest) {
              addAnn(lr, idx, idx + 2, "wsmd-marker");
              addAnn(lr, idx + 2, imgClose, "wsmd-img-alt");
              addAnn(lr, imgClose, imgDest.closePos + 1, "wsmd-marker");
              if (imgDest.urlEnd > imgDest.urlStart) addAnn(lr, imgDest.urlStart, imgDest.urlEnd, "wsmd-link-url");
              idx = imgDest.closePos + 1;
              continue;
            }
          }
        }

        // 5. Attachment markers ([Image #N], [Text #N]) excluded from link parsing
        if (ch === "[") {
          var bracketSub = text.slice(idx, end);
          var attM = /^\[(?:Image|Text) #[1-9][0-9]{0,5}\]/.exec(bracketSub);
          if (attM) {
            idx += attM[0].length;
            continue;
          }
          var closeBr = findClosingBracket(text, idx + 1, end, capsules);
          if (closeBr > idx + 1) {
            // 5a. Inline link: [label](url)
            var dest = parseLinkDest(text, closeBr + 1, end);
            if (dest) {
              addAnn(lr, idx, idx + 1, "wsmd-marker");
              addAnn(lr, idx + 1, closeBr, "wsmd-link");
              scanInline(lr, idx + 1, closeBr, depth + 1);
              addAnn(lr, closeBr, dest.closePos + 1, "wsmd-marker");
              if (dest.urlEnd > dest.urlStart) addAnn(lr, dest.urlStart, dest.urlEnd, "wsmd-link-url");
              idx = dest.closePos + 1;
              continue;
            }
            // 5b. Reference links: [label][ref], [label][], or shortcut [label]
            var labelText = text.slice(idx + 1, closeBr);
            if (closeBr + 1 < end && text.charAt(closeBr + 1) === "[") {
              var refClose = findClosingBracket(text, closeBr + 2, end, capsules);
              if (refClose >= closeBr + 2) {
                var refKey = normalizeRefLabel(refClose === closeBr + 2 ? labelText : text.slice(closeBr + 2, refClose));
                if (refKey && Object.prototype.hasOwnProperty.call(refDefs, refKey)) {
                  addAnn(lr, idx, idx + 1, "wsmd-marker");
                  addAnn(lr, idx + 1, closeBr, "wsmd-link");
                  scanInline(lr, idx + 1, closeBr, depth + 1);
                  addAnn(lr, closeBr, refClose + 1, "wsmd-marker");
                  idx = refClose + 1;
                  continue;
                }
              }
            } else {
              var shortKey = normalizeRefLabel(labelText);
              if (shortKey && Object.prototype.hasOwnProperty.call(refDefs, shortKey)) {
                addAnn(lr, idx, idx + 1, "wsmd-marker");
                addAnn(lr, idx + 1, closeBr, "wsmd-link");
                scanInline(lr, idx + 1, closeBr, depth + 1);
                addAnn(lr, closeBr, closeBr + 1, "wsmd-marker");
                idx = closeBr + 1;
                continue;
              }
            }
          }
        }

        // 6. Bare GFM autolinks: https://..., http://..., www....
        if ((ch === "h" || ch === "w") && (idx === start || /[\s(*_~[]/.test(text.charAt(idx - 1)))) {
          var bareM = /^(?:https?:\/\/|www\.)[^\s<>]+/.exec(text.slice(idx, end));
          if (bareM) {
            var bareStr = bareM[0];
            while (bareStr.length && /[.,;:!?'"\]]/.test(bareStr.charAt(bareStr.length - 1))) {
              bareStr = bareStr.slice(0, -1);
            }
            // Balance trailing closing parens against opening parens inside URL.
            while (bareStr.endsWith(")")) {
              var opens = (bareStr.match(/\(/g) || []).length;
              var closes = (bareStr.match(/\)/g) || []).length;
              if (closes > opens) bareStr = bareStr.slice(0, -1);
              else break;
            }
            if (bareStr.length > 7) {
              addAnn(lr, idx, idx + bareStr.length, "wsmd-link wsmd-autolink");
              idx += bareStr.length;
              continue;
            }
          }
        }

        // 7. Strikethrough ~~text~~
        if (ch === "~" && idx + 2 < end && text.charAt(idx + 1) === "~" && text.charAt(idx + 2) !== "~" && !/[ \t]/.test(text.charAt(idx + 2))) {
          var closeStrike = findClosingDelim(text, idx + 2, end, "~~", false, capsules);
          if (closeStrike > idx + 2) {
            addAnn(lr, idx, idx + 2, "wsmd-marker");
            addAnn(lr, idx + 2, closeStrike, "wsmd-strike");
            addAnn(lr, closeStrike, closeStrike + 2, "wsmd-marker");
            scanInline(lr, idx + 2, closeStrike, depth + 1);
            idx = closeStrike + 2;
            continue;
          }
        }

        // 8. Bold + Italic ***text*** or ___text___
        if ((ch === "*" || ch === "_") && idx + 3 < end && text.charAt(idx + 1) === ch && text.charAt(idx + 2) === ch && text.charAt(idx + 3) !== ch && !/[ \t]/.test(text.charAt(idx + 3))) {
          var wb3 = ch === "_";
          if (!wb3 || idx === start || !isWordChar(text.charAt(idx - 1))) {
            var closeBI = findClosingDelim(text, idx + 3, end, ch + ch + ch, wb3, capsules);
            if (closeBI > idx + 3) {
              addAnn(lr, idx, idx + 3, "wsmd-marker");
              addAnn(lr, idx + 3, closeBI, "wsmd-strong wsmd-em");
              addAnn(lr, closeBI, closeBI + 3, "wsmd-marker");
              scanInline(lr, idx + 3, closeBI, depth + 1);
              idx = closeBI + 3;
              continue;
            }
          }
        }

        // 9. Bold **text** or __text__
        if ((ch === "*" || ch === "_") && idx + 2 < end && text.charAt(idx + 1) === ch && !/[ \t]/.test(text.charAt(idx + 2))) {
          var wb2 = ch === "_";
          if (!wb2 || idx === start || !isWordChar(text.charAt(idx - 1))) {
            var closeBold = findClosingDelim(text, idx + 2, end, ch + ch, wb2, capsules);
            if (closeBold > idx + 2) {
              addAnn(lr, idx, idx + 2, "wsmd-marker");
              addAnn(lr, idx + 2, closeBold, "wsmd-strong");
              addAnn(lr, closeBold, closeBold + 2, "wsmd-marker");
              scanInline(lr, idx + 2, closeBold, depth + 1);
              idx = closeBold + 2;
              continue;
            }
          }
        }

        // 10. Italic *text* or _text_
        if ((ch === "*" || ch === "_") && idx + 1 < end && text.charAt(idx + 1) !== ch && !/[ \t]/.test(text.charAt(idx + 1))) {
          var wb1 = ch === "_";
          if (!wb1 || idx === start || !isWordChar(text.charAt(idx - 1))) {
            var closeEm = findClosingDelim(text, idx + 1, end, ch, wb1, capsules);
            if (closeEm > idx + 1) {
              addAnn(lr, idx, idx + 1, "wsmd-marker");
              addAnn(lr, idx + 1, closeEm, "wsmd-em");
              addAnn(lr, closeEm, closeEm + 1, "wsmd-marker");
              scanInline(lr, idx + 1, closeEm, depth + 1);
              idx = closeEm + 1;
              continue;
            }
          }
        }

        idx++;
      }
    }

    for (var li = 0; li < raw.length; li++) {
      var lineRec = raw[li];
      for (var ir = 0; ir < lineRec.inlineRanges.length; ir++) {
        scanInline(lineRec, lineRec.inlineRanges[ir][0], lineRec.inlineRanges[ir][1], 0);
      }
    }

    // 4. Overlay @ file mentions outside fenced/indented/inline code and capsules.
    var allCodeRanges = codeRanges.concat(inlineCodeRanges);
    var capsuleIntervals = capsules.map(function (c) { return [c.start, c.end]; });
    if (Array.isArray(mentionList) && mentionList.length) {
      for (var mi = 0; mi < mentionList.length; mi++) {
        var m = mentionList[mi];
        if (!m || !Number.isInteger(m.start) || !Number.isInteger(m.end) || m.start < 0 || m.end <= m.start || m.end > len) continue;
        if (typeof m.text === "string" && text.slice(m.start, m.end) !== m.text) continue;
        if (overlapsRangeList(allCodeRanges, m.start, m.end)) continue;
        if (overlapsRangeList(capsuleIntervals, m.start, m.end)) continue;
        for (var lj = 0; lj < raw.length; lj++) {
          var ln = raw[lj];
          if (ln.end <= m.start) continue;
          if (ln.start >= m.end) break;
          addAnn(ln, Math.max(ln.start, m.start), Math.min(ln.end, m.end), "wsmd-mention");
        }
      }
    }

    // 5. Flatten annotations per line into non-overlapping leaf tokens.
    var lines = new Array(raw.length);
    for (var k = 0; k < raw.length; k++) {
      var r = raw[k];
      lines[k] = {
        start: r.start,
        end: r.end,
        hasNewline: r.hasNewline,
        kind: r.kind,
        blockClass: r.blockClasses.join(" "),
        qDepth: r.qDepth,
        indent: r.indent,
        tokens: flattenTokens(r.start, r.end, r.annotations)
      };
    }
    return { lines: lines, refDefs: refDefs };
  }

  window.WsMdParser = {
    parse: parseDocument
  };
})();
