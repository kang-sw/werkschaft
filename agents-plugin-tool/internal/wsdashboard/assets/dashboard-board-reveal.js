  // --- board reveal: re-measure #bcol and settle once it is visible again ----
  // A frame that loads or stays hidden (shell session switch, background tab,
  // bfcache) lays #bcol out at 0px, so free-canvas windows and card heights come
  // from the fallback cache and the middle viewport stays blank until the user
  // scrolls. No ResizeObserver entry reports the later reveal when the box
  // keeps its size, so each discrete visible-again event re-reads the box and
  // settles once. Scheduling is coalesced into one double-rAF pass (display and
  // iframe reveal commit in the first frame); nothing here runs per scroll
  // tick or per frame.
  var boardRevealRaf = 0;
  function boardRevealHidden() {
    return !!((typeof docHidden === "function" ? docHidden() : doc.hidden) || (doc.documentElement && doc.documentElement.hasAttribute && doc.documentElement.hasAttribute("data-animation-paused"))) ||
      (typeof shHidden !== "undefined" && !!shHidden);
  }
  // boardRevealRemeasure re-reads #bcol's content box and overflow after a
  // hidden spell. A box still without size keeps the last valid sizes.
  function boardRevealRemeasure(col) {
    var cs = getComputedStyle(col), h = col.clientHeight, w = col.clientWidth;
    if (!(h > 0 && w > 0)) return false;
    var padTop = parseFloat(cs.paddingTop) || 0;
    anchorSized.bounded = /auto|scroll/.test(cs.overflowY);
    anchorSized.w = w - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
    anchorSized.h = anchorSized.bounded ? h - padTop - (parseFloat(cs.paddingBottom) || 0) : 0;
    anchorSized.padTop = padTop;
    anchorApplySizes(col);
    return true;
  }
  function boardRevealNow() {
    var col = $("bcol");
    if (boardRevealHidden() || !col || !window.WsBoardAnchor) return;
    if (typeof freeHeightCache !== "undefined") freeHeightCache._skippedHidden = false;
    boardRevealRemeasure(col);
    window.WsBoardAnchor.settle();
  }
  // boardRevealSchedule queues one post-layout remeasure+settle, superseding a
  // pending one. freeOnly limits it to boards in free-canvas mode (filter, tab
  // and view transitions), where a stale window shows blank.
  function boardRevealSchedule(freeOnly) {
    if (typeof requestAnimationFrame !== "function") return;
    if (freeOnly) {
      var col = $("bcol");
      if (!col || !window.WsBoardAnchor || !freeEnabled(col, anchorCards())) return;
    }
    if (boardRevealRaf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(boardRevealRaf);
    boardRevealRaf = requestAnimationFrame(function () {
      boardRevealRaf = requestAnimationFrame(function () {
        boardRevealRaf = 0;
        boardRevealNow();
      });
    });
  }
  function boardRevealInit() {
    // The shell's show/hide and the browser's own visibility both arrive as
    // ws-page-visibility: re-measure and settle on every hidden-to-visible edge.
    doc.addEventListener("ws-page-visibility", boardRevealNow);
    doc.addEventListener("visibilitychange", function () { if (!(typeof docHidden === "function" ? docHidden() : doc.hidden)) boardRevealSchedule(); });
    if (window.addEventListener) window.addEventListener("pageshow", function () { boardRevealSchedule(); });
  }
