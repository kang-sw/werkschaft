(function () {
  "use strict";

  const tr = (...args) => window.wsT(...args);
  const profileLabels = {
    description: "js.settings.profile.description", backend: "js.settings.profile.backend",
    provider: "js.settings.profile.provider", model: "js.settings.profile.model",
    authority: "js.settings.profile.authority", tools: "js.settings.profile.tools",
    ws_tools: "js.settings.profile.ws_tools", sparse: "js.settings.profile.sparse",
    preserve: "js.settings.profile.preserve", hooks: "js.settings.profile.hooks"
  };

  function node(tag, className, text) {
    const el = document.createElement(tag);
    if (className) el.className = className;
    if (text !== undefined) el.textContent = text;
    return el;
  }

  function undoIcon() {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "i");
    svg.setAttribute("aria-hidden", "true");
    const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#i-undo");
    svg.append(use);
    return svg;
  }

  function action(name, label, className) {
    const button = node("button", "btn " + (className || ""), label);
    button.type = "button";
    button.dataset.settingsProposalAction = name;
    return button;
  }

  function lineDiff(original, edited) {
    const before = original === "" ? [] : original.split("\n");
    const after = edited === "" ? [] : edited.split("\n");
    const result = [];
    let head = 0;
    while (head < before.length && head < after.length && before[head] === after[head]) {
      result.push(["ctx", before[head++]]);
    }
    let tail = 0;
    while (tail < before.length - head && tail < after.length - head &&
           before[before.length - tail - 1] === after[after.length - tail - 1]) tail++;
    const a = before.slice(head, before.length - tail);
    const b = after.slice(head, after.length - tail);
    if (a.length && b.length && a.length * b.length <= 250000) {
      const table = Array.from({ length: a.length + 1 }, () => new Uint32Array(b.length + 1));
      for (let i = a.length - 1; i >= 0; i--) {
        for (let j = b.length - 1; j >= 0; j--) {
          table[i][j] = a[i] === b[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
        }
      }
      let i = 0, j = 0;
      while (i < a.length || j < b.length) {
        if (i < a.length && j < b.length && a[i] === b[j]) {
          result.push(["ctx", a[i++]]);
          j++;
        } else if (i < a.length && (j === b.length || table[i + 1][j] >= table[i][j + 1])) {
          result.push(["del", a[i++]]);
        } else {
          result.push(["add", b[j++]]);
        }
      }
    } else {
      for (const line of a) result.push(["del", line]);
      for (const line of b) result.push(["add", line]);
    }
    for (let i = before.length - tail; i < before.length; i++) result.push(["ctx", before[i]]);
    return result;
  }

  const prefixes = { ctx: "  ", add: "+ ", del: "− " };
  const modalQuery = new URLSearchParams(location.search);
  const modal = modalQuery.get("frame") === "1" && modalQuery.get("modal") === "1";
  const host = window.parent;
  const drafts = host.wsSettingsRepoDrafts || (host.wsSettingsRepoDrafts = new Map());
  const draftGroups = host.wsSettingsRepoDraftGroups || (host.wsSettingsRepoDraftGroups = new WeakMap());
  if (!host.wsSettingsRepoDraftGuard) {
    host.wsSettingsRepoDraftGuard = true;
    host.addEventListener("beforeunload", event => {
      if (![...drafts.values()].some(draft => draft.size > 0)) return;
      event.preventDefault();
      event.returnValue = "";
    });
  }
  // The shell's native close check asks every frame. Repo proposals live
  // only in the shell's memory, so a clean Settings frame is ready to close.
  window.wsPersistDraftForWindowClose = async () => ![...drafts.values()].some(draft => draft.size > 0);
  let refresh = null;
  let doRerender = null;
  let activePrompt = "preference.*";

  function pending(entry, selection) {
    return selection.scope === "repo" && drafts.get(selection.target)?.get(identity(entry));
  }

  function discardEntryDraft(entry, selection, rerender = true) {
    if (selection.scope !== "repo" || !selection.target) return;
    const draft = drafts.get(selection.target);
    const operation = draft?.get(identity(entry));
    for (const item of draftGroups.get(operation) || (operation ? [operation] : [])) {
      if (draft.get(identity(item)) === item) draft.delete(identity(item));
    }
    if (rerender && doRerender) doRerender();
    if (refresh) refresh();
  }

  function queue(operations, selection, rerender = false) {
    const draft = targetDraft(selection.target);
    for (const operation of operations) {
      if (operations.length > 1) draftGroups.set(operation, operations);
      draft.set(identity(operation), operation);
    }
    if (rerender && doRerender) doRerender();
    if (refresh) refresh();
  }

  function markDraft(row, operation, entry, selection) {
    row.toggleAttribute("data-settings-draft", !!operation);
    const prov = row.querySelector(".kprov");
    const pill = prov?.querySelector(".origin-pill");
    const serverReset = prov?.querySelector(".reset-btn:not(.settings-draft-discard)");
    let discard = prov?.querySelector(".settings-draft-discard");
    if (!operation) {
      discard?.remove();
      if (pill && pill.dataset.draftOrigLabel !== undefined) {
        const lbl = pill.querySelector(".lbl");
        if (lbl) lbl.textContent = pill.dataset.draftOrigLabel;
        pill.className = pill.dataset.draftOrigClass || "origin-pill";
        delete pill.dataset.draftOrigLabel;
        delete pill.dataset.draftOrigClass;
      }
      if (serverReset) serverReset.hidden = false;
      row._syncDrawer?.();
      return;
    }
    if (pill) {
      if (pill.dataset.draftOrigLabel === undefined) {
        pill.dataset.draftOrigLabel = pill.querySelector(".lbl")?.textContent || "";
        pill.dataset.draftOrigClass = pill.className;
      }
      const lbl = pill.querySelector(".lbl");
      if (lbl) lbl.textContent = tr(operation.action === "reset" ? "js.settings.proposal.reset" : "js.settings.proposal.queued");
      pill.className = "origin-pill draft";
    }
    if (serverReset) serverReset.hidden = true;
    if (!discard && prov && entry && selection) {
      discard = node("button", "reset-icon-btn reset-btn settings-draft-discard");
      discard.type = "button";
      const label = tr("js.settings.proposal.discard");
      discard.title = label;
      discard.setAttribute("aria-label", label);
      discard.append(undoIcon());
      discard.addEventListener("click", () => discardEntryDraft(entry, selection));
      prov.append(discard);
    }
    row._syncDrawer?.();
  }

  function identity(operation) {
    const selectors = Object.entries(operation.selectors || {}).sort(([a], [b]) => a.localeCompare(b));
    return JSON.stringify([operation.key, selectors]);
  }

  function targetDraft(target) {
    if (!drafts.has(target)) drafts.set(target, new Map());
    return drafts.get(target);
  }

  function patchText(operations) {
    return operations.map(op => {
      const suffix = Object.values(op.selectors || {}).map(value => value === "*" ? "all" : value).join("/");
      const key = suffix ? op.key + " [" + suffix + "]" : op.key;
      if (op.action === "reset") return "− " + key;
      return "+ " + key + " = " + JSON.stringify(op.value);
    }).join("\n");
  }

  function profilePill(label, valueText, titleText) {
    const pill = node("span", "ppill");
    if (titleText) pill.title = titleText;
    pill.append(node("b", "", label), node("code", "", valueText));
    return pill;
  }

  function summarizeProfileField(key, raw) {
    if (Array.isArray(raw)) {
      return { text: String(raw.length), title: raw.join(", ") };
    }
    if (raw && typeof raw === "object") {
      const keys = Object.keys(raw);
      return { text: String(keys.length), title: JSON.stringify(raw) };
    }
    if (typeof raw === "boolean") {
      return { text: tr(raw ? "js.settings.on" : "js.settings.off"), title: "" };
    }
    return { text: String(raw), title: "" };
  }

  function renderRow({ entry, selection, row, control, syncDrawer }) {
    row.dataset.settingsDraftIdentity = identity(entry);
    row._draftEntry = entry;
    row._draftSelection = selection;
    row._syncDrawer = syncDrawer;
    markDraft(row, pending(entry, selection), entry, selection);
    if (entry.key.startsWith("prompt.")) {
      const editor = control.querySelector("textarea") || row.querySelector("textarea");
      if (!editor) return;
      const seed = typeof entry.seed === "string" ? entry.seed : "";
      const layout = node("div", "prompt-panes");
      const original = node("div", "pane-box");
      original.append(node("span", "ptitle", tr("js.settings.prompt.seed")), node("pre", "seed-pre", seed));
      const override = node("div", "pane-box");
      override.append(node("span", "ptitle", tr("js.settings.prompt.override")));
      if (selection.scope === "repo") {
        const draft = drafts.get(selection.target)?.get(identity(entry));
        if (draft?.action === "set") editor.value = draft.value;
        if (draft?.action === "reset") editor.value = seed;
      }
      if (selection.scope === "builtin") editor.value = seed;
      editor.classList.add("prompt-ta");
      editor.dataset.settingsPromptEditor = "1";
      editor.setAttribute("aria-label", tr("js.settings.prompt.override"));
      override.append(editor);
      layout.append(original, override);

      const diff = node("div", "pane-box settings-prompt-diff");
      const text = node("pre", "diff-box");
      text.dataset.settingsPromptDiff = "1";
      text.setAttribute("aria-label", tr("js.settings.prompt.diff"));
      diff.append(node("span", "ptitle", tr("js.settings.prompt.diff")), text);
      function update() {
        const lines = lineDiff(seed, editor.value);
        const fragment = document.createDocumentFragment();
        let last = "", group = [];
        function flush() {
          if (group.length) fragment.append(node("span", "diff-line " + last, group.join("\n") + "\n"));
        }
        for (const [kind, line] of lines) {
          if (kind !== last) {
            flush();
            group = [];
            last = kind;
          }
          group.push(prefixes[kind] + line);
        }
        flush();
        text.replaceChildren(fragment);
      }
      editor.addEventListener("input", update);
      update();

      const meta = row.querySelector(".kmeta");
      const rail = row.querySelector(".sctl-rail");
      const ctlWrap = row.querySelector(".kctl-wrap");
      const prov = row.querySelector(".kprov");
      const saveBtn = control.querySelector(".save-btn");
      const status = ctlWrap?.querySelector(".hint");
      const drawer = row.querySelector(".layer-drawer");

      control.replaceChildren(layout, diff);
      const headRight = node("div", "prompt-head-right");
      if (saveBtn) {
        if (!control.id) {
          control.id = "prompt-form-" + identity(entry).replace(/[^a-z0-9_-]+/gi, "-");
        }
        saveBtn.setAttribute("form", control.id);
        headRight.append(saveBtn);
      }
      if (status) headRight.append(status);
      if (prov) headRight.append(prov);
      const head = node("div", "prompt-head");
      if (meta) head.append(meta);
      head.append(headRight);
      const body = node("div", "prompt-body");
      body.append(control);
      rail?.remove();
      row.classList.remove("srow");
      row.classList.add("prompt-card");
      row.replaceChildren(head);
      if (drawer) row.append(drawer);
      row.append(body);
    } else if (entry.key.startsWith("agent.profile.") || entry.key.startsWith("worktree.profile.")) {
      const profile = selection.scope === "builtin" ? entry.default : entry.value;
      const meta = row.querySelector(".kmeta");
      const rail = row.querySelector(".sctl-rail");
      const prov = row.querySelector(".kprov");
      const drawer = row.querySelector(".layer-drawer");
      const labelLine = meta?.querySelector(".klabel-line");
      const descNode = meta?.querySelector(".kdesc");
      const descText = (profile && typeof profile === "object" && typeof profile.description === "string") ? profile.description : descNode?.textContent;

      const head = node("div", "prof-head");
      const titleWrap = node("div", "prof-head-title");
      if (labelLine) titleWrap.append(...labelLine.childNodes);
      head.append(titleWrap);
      if (prov) head.append(prov);

      const pills = node("div", "prof-pills");
      if (profile && typeof profile === "object") {
        const fields = entry.key.startsWith("agent.profile.") ?
          ["backend", "provider", "model", "authority", "tools", "ws_tools"] :
          ["sparse", "preserve", "hooks"];
        for (const key of fields) {
          if (profile[key] === undefined) continue;
          const summary = summarizeProfileField(key, profile[key]);
          pills.append(profilePill(tr(profileLabels[key]), summary.text, summary.title));
        }
      }
      const details = node("details", "prof-details");
      details.dataset.settingsProfileJson = "1";
      const summary = node("summary", "prof-json-btn", tr("js.settings.profile.json"));
      const raw = node("pre", "prof-json", JSON.stringify(profile, null, 2));
      details.append(summary, raw);

      rail?.remove();
      row.classList.remove("srow");
      row.classList.add("prof-card");
      row.replaceChildren(head);
      if (descText) row.append(node("p", "kdesc", descText));
      if (pills.children.length) row.append(pills);
      row.append(details);
      if (drawer) row.append(drawer);
    }
  }

  function syncPromptPointDot(button, key, entries, bucketState, bucketTitle) {
    button.querySelector(".bdot")?.remove();
    button.removeAttribute("data-bucket-override");
    button.removeAttribute("title");
    if (!entries || !bucketState) return;
    const st = bucketState(entries.filter(entry => key === "preference.*"
      ? window.WsSettingsPreferences.replaces(entry.key) : entry.key === key));
    if (st.kind === "none") return;
    button.dataset.bucketOverride = st.kind;
    const dot = node("span", "bdot " + st.kind);
    dot.setAttribute("aria-hidden", "true");
    button.append(dot);
    if (bucketTitle) button.title = bucketTitle(key, st);
  }

  function afterRender({ section, list, entries, bucketState, bucketTitle }) {
    if (section === "profiles") {
      for (const family of ["worktree", "agent"]) {
        const rows = [...list.children].filter(row => row.dataset.configKey?.startsWith(family + ".profile."));
        if (!rows.length) continue;
        const group = node("section", "settings-profile-family");
        group.append(node("h3", "", tr(family === "worktree" ? "js.settings.profile.family.worktree" : "js.settings.profile.family.agent")));
        const grid = node("div", "prof-grid");
        grid.append(...rows);
        group.append(grid);
        list.append(group);
      }
    }
    if (section !== "prompts") return;
    const cards = [...list.children].filter(row => row.dataset.settingsPromptCard || row.dataset.configKey?.startsWith("prompt."));
    if (!cards.length) return;
    const point = row => row.dataset.settingsPromptCard || row.dataset.configKey;
    const active = () => cards.some(row => point(row) === activePrompt) ? activePrompt : point(cards[0]);
    const picker = node("div", "prompt-picker");
    picker.setAttribute("role", "group");
    picker.setAttribute("aria-label", tr("js.settings.prompt.points"));
    const buttons = cards.map(row => {
      const key = point(row);
      const button = node("button", "fchip");
      button.type = "button";
      button.dataset.settingsPromptPoint = key;
      if (key === "preference.*") button.append(tr("js.settings.preference.title") + " ");
      button.append(node("code", "", key));
      syncPromptPointDot(button, key, entries, bucketState, bucketTitle);
      button.addEventListener("click", () => {
        activePrompt = key;
        apply();
      });
      return button;
    });
    function apply() {
      buttons.forEach((button, index) => {
        const selected = button.dataset.settingsPromptPoint === active();
        button.setAttribute("aria-pressed", String(selected));
        cards[index].hidden = !selected;
      });
    }
    picker.append(...buttons);
    list.prepend(picker);
    apply();
  }

  function onMutate({ section, list, entries, bucketState, bucketTitle }) {
    if (section !== "prompts" || !list) return;
    for (const button of list.querySelectorAll(".prompt-picker [data-settings-prompt-point]")) {
      syncPromptPointDot(button, button.dataset.settingsPromptPoint, entries, bucketState, bucketTitle);
    }
  }

  function mount({ proposal, selection, snapshot, rerender }) {
    doRerender = rerender;
    if (!proposal || selection.scope !== "repo" || !selection.target) return;
    const target = selection.target;
    const draft = targetDraft(target);
    let expanded = false;
    let message = "";
    let status = "";
    let waiting = false;
    let noLead = false;
    let manual = "";

    window.wsHoldsDraft = () => !modal && draft.size > 0;
    window.wsPrepareSessionLeave = window.wsHoldsDraft;

    function payload() {
      return { target, operations: [...draft.values()] };
    }

    function render() {
      for (const row of document.querySelectorAll("[data-settings-draft-identity]")) {
        markDraft(row, draft.get(row.dataset.settingsDraftIdentity), row._draftEntry, row._draftSelection);
      }
      proposal.replaceChildren();
      proposal.hidden = !draft.size && !message;
      if (proposal.hidden) return;
      const footer = node("footer", "proposal-bar");
      footer.setAttribute("aria-label", tr("js.settings.proposal.title"));
      const top = node("div", "proposal-top");
      const title = node("div", "proposal-title");
      title.append(node("span", "origin-pill draft", tr("js.settings.proposal.title")));
      if (draft.size) {
        const path = node("code", "skey", ".ws-workflow/config.json");
        path.title = snapshot.path;
        title.append(path, node("span", "", tr("js.settings.proposal.count", draft.size)));
      }
      top.append(title);

      if (draft.size) {
        const toggle = action("toggle", tr(expanded ? "js.settings.proposal.hide" : "js.settings.proposal.show"));
        toggle.setAttribute("aria-expanded", String(expanded));
        toggle.addEventListener("click", () => {
          expanded = !expanded;
          render();
          proposal.querySelector('[data-settings-proposal-action="toggle"]').focus();
        });
        const clear = action("clear", tr("js.settings.proposal.clear"));
        clear.disabled = waiting;
        clear.addEventListener("click", () => {
          draft.clear();
          message = status = manual = "";
          rerender();
          render();
        });
        top.append(toggle, clear);
        const controls = node("div", "proposal-actions");
        const send = action("send", tr(waiting ? "js.settings.proposal.sending" : "js.settings.proposal.send"), "pri");
        send.disabled = waiting;
        send.addEventListener("click", sendProposal);
        const copy = action("copy", tr("js.settings.proposal.copy"), noLead ? "pri" : "");
        copy.addEventListener("click", copyProposal);
        controls.append(send, copy);
        top.append(controls);
      }
      footer.append(top);
      if (expanded && draft.size) footer.append(node("pre", "proposal-patch", patchText(payload().operations)));
      if (message) {
        const result = node("div", "settings-proposal-status", message);
        result.dataset.settingsProposalStatus = status;
        result.setAttribute("role", "status");
        footer.append(result);
      }
      if (manual) {
        const label = node("label", "settings-manual-copy", tr("js.settings.proposal.manual"));
        const text = node("textarea", "proposal-patch");
        text.readOnly = true;
        text.value = manual;
        text.dataset.settingsProposalManual = "1";
        label.append(text);
        footer.append(label);
      }
      proposal.append(footer);
    }

    async function sendProposal() {
      const request = payload();
      const sent = [...draft.entries()];
      waiting = true;
      message = status = "";
      render();
      try {
        const response = await fetch("/api/config/proposal", {
          method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request)
        });
        const body = await response.json();
        if (!response.ok || body.delivered !== true) {
          noLead = body.error?.code === "no_active_lead";
          status = noLead ? "no_active_lead" : "failed";
          message = tr(noLead ? "js.settings.proposal.no_lead" : "js.settings.proposal.failed");
        } else {
          for (const [key, operation] of sent) {
            if (draft.get(key) === operation) draft.delete(key);
          }
          message = tr("js.settings.proposal.sent");
          status = "delivered";
          noLead = false;
          manual = "";
          rerender();
        }
      } catch {
        message = tr("js.settings.proposal.failed");
        status = "failed";
      } finally {
        waiting = false;
        render();
      }
    }

    async function copyProposal() {
      const request = payload();
      const text = JSON.stringify({ ...request, path: snapshot.path,
        request: "Review and, if appropriate, apply these settings to this worktree's repo config." }, null, 2) + "\n\n" + patchText(request.operations);
      try {
        await navigator.clipboard.writeText(text);
        if (!noLead) {
          message = tr("js.settings.proposal.copied");
          status = "copied";
        }
        manual = "";
        render();
      } catch {
        manual = text;
        render();
        const area = proposal.querySelector("[data-settings-proposal-manual]");
        area.focus();
        area.select();
      }
    }

    refresh = render;
    render();
  }

  window.WsSettingsExtension = {
    editable(entry, selection) { return selection.scope === "repo" && !!selection.target && entry.editable; },
    hasDraft(entry, selection) { return pending(entry, selection); },
    value(entry, selection, value) {
      const draft = pending(entry, selection);
      if (draft?.action === "set") return draft.value;
      if (draft?.action === "reset") return entry.default;
      return value;
    },
    renderRow,
    afterRender,
    onMutate,
    mount,
    discard(entry, selection) { return discardEntryDraft(entry, selection, false); },
    operations(selection) {
      return selection.scope === "repo" ? [...(drafts.get(selection.target)?.values() || [])] : [];
    },
    queue,
    submit(operation, selection) {
      if (selection.scope !== "repo" || !selection.target) return undefined;
      queue([operation], selection, true);
      return Promise.resolve({});
    }
  };
})();
