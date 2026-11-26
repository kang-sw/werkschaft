// Composer live Markdown integration: wires #ctext's .wsc-editor to the
// element-scoped window.WsMdEditor component while preserving shell-mode `!`
// highlighting (ws-bang) and @ file-mention overlays.
  var mdComposing = false, mdEditor = null;

  function ensureComposerMdEditor() {
    if (mdEditor) return mdEditor;
    var editor = ctext && ctext.wsc && ctext.wsc.editor;
    if (!editor || !window.WsMdEditor || typeof window.WsMdEditor.attach !== "function") return null;
    mdEditor = window.WsMdEditor.attach(editor, {
      getMentions: function () { return typeof mentions !== "undefined" && Array.isArray(mentions) ? mentions : []; },
      isBang: function () { return typeof bangMode === "function" ? !!bangMode() : false; },
      isComposing: function () { return mdComposing; },
      onReconciled: function () {
        if (typeof bangMode === "function" && bangMode() && typeof bangPaint === "function") bangPaint(true);
      }
    });
    return mdEditor;
  }

  function clearComposerHighlights() {
    var inst = ensureComposerMdEditor();
    if (inst && typeof inst.reconcile === "function") inst.reconcile();
  }

  function applyComposerHighlights() {
    var inst = ensureComposerMdEditor();
    if (inst && typeof inst.reconcile === "function") inst.reconcile();
  }

  function scheduleComposerHighlights() {
    if (mdComposing) return;
    var inst = ensureComposerMdEditor();
    if (!inst) return;
    if (typeof inst.schedule === "function") inst.schedule();
    else if (typeof inst.reconcile === "function") inst.reconcile();
  }

  if (ctext) {
    ensureComposerMdEditor();
    ctext.addEventListener("compositionstart", function () { mdComposing = true; });
    ctext.addEventListener("compositionend", function () { mdComposing = false; scheduleComposerHighlights(); });
  }
