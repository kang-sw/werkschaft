// Pinned topic-panel card kind for the dashboard gallery (WsCards "panel").
// Mirrors a whiteboard topic's latest message panel (pN) into a gallery card
// using the existing .doc-frame pipeline without duplicating id="pN"/"eN".
(function () {
  "use strict";
  var doc = document;
  var hooks = {};
  var FRAME_ATTRS = [
    "data-doc", "data-doc-key", "data-topic", "data-entry", "data-relay",
    "data-sandbox", "data-srcdoc", "data-mockup-src", "data-mockup-sha",
    "data-fallback-text", "title"
  ];

  function t(k, fb) {
    if (typeof window.wsT === "function") {
      var v = window.wsT(k);
      if (v && v !== k) return v;
    }
    return fb || k;
  }

  function boardBoxes() {
    if (hooks.boardBoxes) return hooks.boardBoxes();
    var out = [];
    var b = doc.getElementById("r-board");
    var g = doc.getElementById("bgal");
    if (b) out.push(b);
    if (g) out.push(g);
    return out;
  }

  function panelCards() {
    return window.WsCards && window.WsCards.list ? window.WsCards.list("panel") : [];
  }

  function findCard(panelId) {
    if (!panelId) return null;
    var list = panelCards();
    for (var i = 0; i < list.length; i++) {
      if (list[i].data && list[i].data.panel === panelId) return list[i];
    }
    return null;
  }

  function isPinned(panelId) {
    return !!findCard(panelId);
  }

  function findLiveArticle(panelId) {
    if (!panelId) return null;
    var boxes = boardBoxes();
    for (var i = 0; i < boxes.length; i++) {
      var box = boxes[i];
      if (!box || !box.querySelectorAll) continue;
      var arts = box.querySelectorAll(".wb-entry.panel:not(.historical)");
      for (var j = 0; j < arts.length; j++) {
        var a = arts[j];
        if (a.getAttribute("data-panel") !== panelId) continue;
        if (a.closest && a.closest('.bcard[data-kind="panel"]')) continue;
        return a;
      }
    }
    return null;
  }

  function extractMeta(art, fallbackTopic, panelId) {
    var recapEl = art.querySelector("details.entry-panel > summary > .entry-recap") || art.querySelector(".entry-recap");
    var verEl = art.querySelector("details.entry-panel > summary .pfver") || art.querySelector(".pfver");
    var sec = art.closest && art.closest("section.topic[data-topic]");
    var topicName = (sec && sec.getAttribute("data-topic")) || fallbackTopic || "";
    var tnameEl = sec && sec.querySelector && sec.querySelector(":scope > .tophead > .tbar .tname");
    var topicLabel = (tnameEl && tnameEl.textContent ? tnameEl.textContent.trim() : "") || topicName;
    var recap = (recapEl && recapEl.textContent ? recapEl.textContent.trim() : "") || panelId;
    var ver = verEl && verEl.textContent ? verEl.textContent.trim() : "";
    return {
      name: recap,
      sub: topicLabel,
      badge: ver || panelId,
      topic: topicName
    };
  }

  function updateHeader(card, meta) {
    var prev = card._panelHead;
    if (prev && prev.name === meta.name && prev.sub === meta.sub && prev.badge === meta.badge) return;
    card._panelHead = { name: meta.name, sub: meta.sub, badge: meta.badge };
    card.title({ icon: "file-text", name: meta.name, sub: meta.sub, badge: meta.badge });
    if (meta.topic && card.data && card.data.topic !== meta.topic) {
      card.data.topic = meta.topic;
      if (card.save) card.save();
    }
  }

  function isMockupFrame(srcFrame) {
    if (!srcFrame) return false;
    if (srcFrame.classList && srcFrame.classList.contains("mockup-frame")) return true;
    return !!(srcFrame.getAttribute && srcFrame.getAttribute("data-mockup-src"));
  }

  function buildPlaceholder(srcFrame) {
    var ph = doc.createElement("div");
    var mockup = isMockupFrame(srcFrame);
    ph.className = mockup ? "doc-frame mockup-frame" : "doc-frame";
    for (var i = 0; i < FRAME_ATTRS.length; i++) {
      var attr = FRAME_ATTRS[i];
      var val = srcFrame.getAttribute(attr);
      if (attr === "data-sandbox" && val == null) val = srcFrame.getAttribute("sandbox");
      if (attr === "data-srcdoc" && val == null) val = srcFrame.getAttribute("srcdoc");
      if (attr === "data-mockup-src" && val == null && mockup) val = srcFrame.getAttribute("src");
      if (val != null) ph.setAttribute(attr, val);
    }
    // Never carry canonical DOM ids into the mirror card.
    ph.removeAttribute("id");
    return ph;
  }

  function mountIfConnected(card) {
    if (!card || card._panelClosed || !card.body || !card.body.isConnected) return;
    if (!hooks.mountFrames) return;
    if (card.body.querySelector && card.body.querySelector("div.doc-frame[data-srcdoc], div.doc-frame[data-mockup-src]")) {
      hooks.mountFrames(card.body);
    }
  }

  function showUnavailable(card) {
    if (!card || card._panelClosed) return;
    card._panelMissing = true;
    if (card.body.querySelector && card.body.querySelector(".panel-card-empty")) return;
    card.body.textContent = "";
    var msg = doc.createElement("div");
    msg.className = "panel-card-empty";
    msg.textContent = t("js.panel.unavailable", "Panel is no longer available");
    card.body.appendChild(msg);
  }

  function applyArticle(card, art, fallbackTopic) {
    if (!card || card._panelClosed || !art) return false;
    var panelId = card.data && card.data.panel || "";
    var meta = extractMeta(art, fallbackTopic || (card.data && card.data.topic) || "", panelId);
    updateHeader(card, meta);
    var srcFrame = art.querySelector(".doc-body .doc-frame") || art.querySelector(".doc-frame");
    if (!srcFrame) return false;
    var entry = srcFrame.getAttribute("data-entry") || art.getAttribute("data-entry") || "";
    var srcdoc = srcFrame.getAttribute("data-srcdoc");
    if (srcdoc == null) srcdoc = srcFrame.getAttribute("srcdoc") || "";
    var mockup = isMockupFrame(srcFrame);
    var mockupSrc = srcFrame.getAttribute("data-mockup-src") || "";
    if (!mockupSrc && mockup) mockupSrc = srcFrame.getAttribute("src") || "";
    var mockupSha = srcFrame.getAttribute("data-mockup-sha") || "";
    if (card._panelMounted && card._panelEntry === entry && card._panelSrcdoc === srcdoc && card._panelMockupSrc === mockupSrc && card._panelMockupSha === mockupSha) {
      mountIfConnected(card);
      return true;
    }
    var wrap = doc.createElement("div");
    wrap.className = "doc-body panel-card-doc";
    var ph = buildPlaceholder(srcFrame);
    if (mockup || mockupSrc || (srcFrame.closest && srcFrame.closest(".mockup-wrap"))) {
      var mwrap = doc.createElement("div");
      mwrap.className = "mockup-wrap";
      mwrap.appendChild(ph);
      wrap.appendChild(mwrap);
    } else {
      wrap.appendChild(ph);
    }
    card.body.textContent = "";
    card.body.appendChild(wrap);
    card._panelMounted = true;
    card._panelMissing = false;
    card._panelEntry = entry;
    card._panelSrcdoc = srcdoc;
    card._panelMockupSrc = mockupSrc;
    card._panelMockupSha = mockupSha;
    mountIfConnected(card);
    return true;
  }

  function parseArticleFromTopics(topics, panelId) {
    if (!Array.isArray(topics)) return null;
    for (var i = 0; i < topics.length; i++) {
      var item = topics[i];
      var html = typeof item === "string" ? item : (item && typeof item.html === "string" ? item.html : "");
      if (!html) continue;
      var box = doc.createElement("div");
      box.innerHTML = html;
      var arts = box.querySelectorAll(".wb-entry.panel:not(.historical)");
      for (var j = 0; j < arts.length; j++) {
        if (arts[j].getAttribute("data-panel") === panelId) {
          var topicName = (item && item.name) || "";
          if (!topicName) {
            var sec = arts[j].closest && arts[j].closest("section.topic[data-topic]");
            if (sec) topicName = sec.getAttribute("data-topic") || "";
          }
          return { art: arts[j], topic: topicName };
        }
      }
    }
    return null;
  }

  function hydrateRemote(card) {
    var panelId = card.data && card.data.panel;
    if (!panelId || card._panelFetching || card._panelClosed) return;
    if (typeof window.wbHistoryFetch !== "function") {
      showUnavailable(card);
      return;
    }
    card._panelFetching = true;
    Promise.resolve().then(function () {
      return window.wbHistoryFetch("target=" + encodeURIComponent(panelId));
    }).then(function (topics) {
      card._panelFetching = false;
      if (card._panelClosed) return;
      var hit = parseArticleFromTopics(topics, panelId);
      if (hit && applyArticle(card, hit.art, hit.topic)) return;
      showUnavailable(card);
    }, function () {
      card._panelFetching = false;
      if (!card._panelClosed && !card._panelMounted) showUnavailable(card);
    });
  }

  function syncCard(card, sourceArt) {
    if (!card || card._panelClosed) return;
    var panelId = card.data && card.data.panel;
    if (!panelId) { showUnavailable(card); return; }
    var art = sourceArt || findLiveArticle(panelId);
    if (art) {
      applyArticle(card, art, card.data && card.data.topic);
      return;
    }
    if (card._panelMounted) {
      mountIfConnected(card);
      return;
    }
    if (!card._panelMissing) hydrateRemote(card);
  }

  function syncButtons() {
    var pinned = {};
    panelCards().forEach(function (c) {
      if (c.data && c.data.panel) pinned[c.data.panel] = true;
    });
    var boxes = boardBoxes();
    for (var i = 0; i < boxes.length; i++) {
      var box = boxes[i];
      if (!box || !box.querySelectorAll) continue;
      var btns = box.querySelectorAll("details.entry-panel > summary > .ppin");
      for (var j = 0; j < btns.length; j++) {
        var b = btns[j];
        var art = b.closest && b.closest(".wb-entry.panel[data-panel]");
        var pid = art && art.getAttribute("data-panel");
        var want = String(!!(pid && pinned[pid]));
        if (b.getAttribute("aria-pressed") !== want) b.setAttribute("aria-pressed", want);
      }
    }
  }

  function sync() {
    var list = panelCards();
    for (var i = 0; i < list.length; i++) syncCard(list[i]);
    syncButtons();
  }

  function toggle(panelId, topicName, sourceArt) {
    if (!panelId || !window.WsCards) return null;
    var existing = findCard(panelId);
    if (existing) {
      existing.close();
      syncButtons();
      return null;
    }
    var card = window.WsCards.open("panel", { panel: panelId, topic: topicName || "" });
    if (!card) return null;
    if (sourceArt) syncCard(card, sourceArt);
    card.pin(true);
    mountIfConnected(card);
    syncButtons();
    return card;
  }

  if (window.WsCards && window.WsCards.register) {
    window.WsCards.register("panel", {
      base: 360,
      unpinCloses: true,
      open: function (card) {
        var panelId = card.data && card.data.panel || "";
        var topicName = card.data && card.data.topic || "";
        card._panelClosed = false;
        card._panelMounted = false;
        card._panelMissing = false;
        card._panelFetching = false;
        card._panelEntry = null;
        card._panelSrcdoc = null;
        card._panelMockupSrc = null;
        card._panelMockupSha = null;
        card._panelHead = null;
        updateHeader(card, { name: panelId, sub: topicName, badge: panelId, topic: topicName });
        card.actions([{
          icon: "external-link",
          cls: "bc-jump",
          title: t("js.panel.jump", "Jump to panel in topic"),
          run: function () {
            if (panelId && typeof window.wbBoardJump === "function") window.wbBoardJump(panelId);
          }
        }]);
        syncCard(card);
        syncButtons();
      },
      pinned: function (card) {
        mountIfConnected(card);
        syncButtons();
      },
      folded: function (card) {
        var s = doc.createElement("span");
        var meta = card._panelHead;
        s.textContent = (meta && meta.sub) || (card.data && card.data.topic) || "";
        return s;
      },
      close: function (card) {
        card._panelClosed = true;
        if (typeof queueMicrotask === "function") queueMicrotask(syncButtons);
        else setTimeout(syncButtons, 0);
      }
    });
  }

  window.WsPanelCard = {
    setup: function (o) { hooks = o || {}; },
    toggle: toggle,
    isPinned: isPinned,
    sync: sync,
    syncButtons: syncButtons
  };
})();
