/* ==========================================================================
   Werkschaft Dashboard — Standalone Git Page (#bgal) Interactive Mockup
   Implements: (1725)=a Wide 3-Region, (1726)=a Review Frontier Jump,
               (1728)=a Narrow 3-Tab Switch, (1729)=a Explicit WalkDir Scan
   ========================================================================== */
(function () {
  "use strict";

  const SVGNS = "http://www.w3.org/2000/svg";
  const GL_ROW = 28;
  const GL_DX = 14;
  const GL_X0 = 12;
  const GL_MORE = 36;
  const FRONTIER_SHA = "4c72b9d";

  // --- Realistic Commit Graph Fixture (Page 1 + Page 2) -------------------
  const COMMITS_PAGE_1 = [
    {
      oid: "8f31a2c904e2b8f71a6c2d3e9a01b4f5c7e8d9a1",
      short: "8f31a2c",
      subject: "feat(dashboard): add standalone Git page container breakpoints",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "6m",
      date: "2026-11-28 11:30",
      col: 0,
      color: 0,
      parents: ["e91c40b21a5f"],
      refs: [
        { kind: "branch", name: "develop", text: "develop", head: true, ahead: 1 }
      ],
      down: [
        { from: 0, to: 0, color: 0 }
      ],
      body: "## AI Context\n- Landed Track 2B container query breakpoints (Wide >= 720px, Narrow 360-719px).\n- Review ledger pill now triggers smooth jump to review frontier.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-git-page.css",
          status: "A", add: 118, del: 0,
          hunks: [
            {
              header: "@@ -0,0 +1,14 @@", old: 1, new: 1,
              lines: [
                { k: "+", t: [{ c: "c", s: "/* Track 2B: Standalone Git Page inside Always-Open #bgal */" }] },
                { k: "+", t: [{ c: "k", s: ".git-regions" }, { s: " { " }, { c: "kt", s: "display" }, { s: ": grid; " }, { c: "kt", s: "grid-template-columns" }, { s: ": " }, { c: "m", s: "52fr 48fr" }, { s: "; }" }] },
                { k: "+", t: [{ c: "k", s: "@container" }, { s: " bgal-box (" }, { c: "kt", s: "max-width" }, { s: ": " }, { c: "m", s: "719px" }, { s: ") {" }] },
                { k: "+", t: [{ s: "  .git-narrow-tabs { " }, { c: "kt", s: "display" }, { s: ": flex; }" }] },
                { k: "+", t: [{ s: "}" }] }
              ]
            }
          ]
        },
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-git.js",
          status: "M", add: 42, del: 11,
          hunks: [
            {
              header: "@@ -612,8 +612,14 @@ function glJumpToFrontier(sha)", old: 612, new: 612,
              lines: [
                { k: " ", t: [{ c: "k", s: "function" }, { s: " " }, { c: "nf", s: "glJumpToFrontier" }, { s: "(sha) {" }] },
                { k: "-", t: [{ s: "  " }, { c: "k", s: "var" }, { s: " row = glrows.querySelector(" }, { c: "s", s: "'[data-sha=\"' + sha + '\"]'" }, { s: ");" }] },
                { k: "+", t: [{ s: "  " }, { c: "nf", s: "setNarrowTab" }, { s: "(" }, { c: "s", s: "'graph'" }, { s: ");" }] },
                { k: "+", t: [{ s: "  " }, { c: "k", s: "var" }, { s: " row = " }, { c: "nf", s: "ensureCommitRow" }, { s: "(sha);" }] },
                { k: "+", t: [{ s: "  row.scrollIntoView({ behavior: " }, { c: "s", s: "'smooth'" }, { s: ", block: " }, { c: "s", s: "'center'" }, { s: " });" }] },
                { k: " ", t: [{ s: "}" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "e91c40b21a5f7c220b98e13a4b008e7c5a12d401",
      short: "e91c40b",
      subject: "Merge branch 'impl/develop/gallery-git-worktrees' into develop",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "18m",
      date: "2026-11-28 11:18",
      col: 0,
      color: 0,
      parents: ["a40b71e92c3d", "7b2e19c551a0"],
      refs: [
        { kind: "upstream", name: "origin/develop", text: "origin/develop" }
      ],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 0, to: 1, color: 1 }
      ],
      body: "## AI Context\n- Merged manual WalkDir worktree scan API and lightweight debounced list endpoint.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/worktrees.go",
          status: "M", add: 64, del: 18,
          hunks: [
            {
              header: "@@ -88,7 +88,12 @@ func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request)", old: 88, new: 88,
              lines: [
                { k: " ", t: [{ s: "  scan := r.URL.Query().Get(" }, { c: "s", s: `"scan"` }, { s: ") == " }, { c: "s", s: `"1"` }] },
                { k: "-", t: [{ s: "  pool, err := s.readWorktreePool(ctx, true)" }] },
                { k: "+", t: [{ c: "c", s: "  // (1729)=a: never walk disk unless user explicitly triggers refresh scan" }] },
                { k: "+", t: [{ s: "  pool, err := s.readWorktreePool(ctx, scan)" }] },
                { k: " ", t: [{ s: "  writeJSON(w, pool)" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "7b2e19c551a03d7e42a1b8f09c41e21d8b309aa4",
      short: "7b2e19c",
      subject: "feat(worktrees): gate WalkDir disk sizing behind ?scan=1 with 2m cache",
      author: "worker-2b",
      email: "worker@werkschaft.dev",
      age: "24m",
      date: "2026-11-28 11:12",
      col: 1,
      color: 1,
      parents: ["a40b71e92c3d"],
      refs: [
        { kind: "branch", name: "impl/develop/gallery-git-page", text: "gallery-git-page", impl: true, worktree: true }
      ],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 1, to: 0, color: 1 }
      ],
      body: "## AI Context\n- Added 2-minute TTL cache for disk byte totals so Windows AV scan overhead stays zero during routine tab switches.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/worktrees.go",
          status: "M", add: 58, del: 12,
          hunks: [
            {
              header: "@@ -142,6 +142,10 @@ func measureWorktreeBytes(root string) int64", old: 142, new: 142,
              lines: [
                { k: " ", t: [{ c: "k", s: "func" }, { s: " " }, { c: "nf", s: "measureWorktreeBytes" }, { s: "(root " }, { c: "kt", s: "string" }, { s: ") " }, { c: "kt", s: "int64" }, { s: " {" }] },
                { k: "+", t: [{ s: "  " }, { c: "k", s: "if" }, { s: " cached, ok := sizeCache.Get(root, " }, { c: "m", s: "2" }, { s: "*time.Minute); ok {" }] },
                { k: "+", t: [{ s: "    " }, { c: "k", s: "return" }, { s: " cached" }] },
                { k: "+", t: [{ s: "  }" }] },
                { k: " ", t: [{ s: "  " }, { c: "k", s: "var" }, { s: " total " }, { c: "kt", s: "int64" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "a40b71e92c3d8a71b94e0c55f1d3a2c88e64b019",
      short: "a40b71e",
      subject: "feat(git): expose review ledger frontier summary on /git/status",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "41m",
      date: "2026-11-28 10:55",
      col: 0,
      color: 0,
      parents: ["c18d93f70a1b"],
      refs: [],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 0, to: 2, color: 2 }
      ],
      body: "## AI Context\n- Added merges_since, lines_add, lines_del, verdict, and frontier_head fields to GitStatusResponse (T0 additive).",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/gitdrawer.go",
          status: "M", add: 39, del: 3,
          hunks: [
            {
              header: "@@ -34,6 +34,12 @@ type GitStatusResponse struct", old: 34, new: 34,
              lines: [
                { k: " ", t: [{ s: "  Ahead     " }, { c: "kt", s: "int" }, { s: " " }, { c: "s", s: "`json:\"ahead\"`" }] },
                { k: "+", t: [{ s: "  Review    *ReviewLedgerSummary " }, { c: "s", s: "`json:\"review,omitempty\"`" }] },
                { k: " ", t: [{ s: "}" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "55d8e2a190bc4417f9e20a7b11c3d8e44b19a6f2",
      short: "55d8e2a",
      subject: "wip(auth): one-click account slot selector popover states",
      author: "worker-1",
      email: "worker@werkschaft.dev",
      age: "52m",
      date: "2026-11-28 10:44",
      col: 2,
      color: 2,
      parents: ["c18d93f70a1b"],
      refs: [
        { kind: "branch", name: "impl/develop/account-slots", text: "account-slots", impl: true, worktree: true }
      ],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 2, to: 0, color: 2 }
      ],
      body: "## AI Context\n- Draft UI for upward slot-switch popover from bottom bar usage pill.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-usage.js",
          status: "M", add: 48, del: 9,
          hunks: [
            {
              header: "@@ -22,4 +22,8 @@ function openUsagePopover()", old: 22, new: 22,
              lines: [
                { k: " ", t: [{ s: "  pop.hidden = " }, { c: "k", s: "false" }, { s: ";" }] },
                { k: "+", t: [{ s: "  pop.dataset.anchor = " }, { c: "s", s: "'bottom-right'" }, { s: ";" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "c18d93f70a1b6e49d2038f5a1c77b84e9021d33f",
      short: "c18d93f",
      subject: "Merge branch 'impl/develop/gallery-ticketmap' into develop",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "1h",
      date: "2026-11-28 10:15",
      col: 0,
      color: 0,
      parents: ["93a1f0e88d42", "2d6b44e119a7"],
      refs: [],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 0, to: 1, color: 4 }
      ],
      body: "## AI Context\n- Landed Track 2A always-open #bgal and docked Ticket Map.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail.css",
          status: "M", add: 94, del: 26,
          hunks: [
            {
              header: "@@ -101,5 +101,9 @@", old: 101, new: 101,
              lines: [
                { k: "-", t: [{ s: ".bgal { display: none; }" }] },
                { k: "+", t: [{ s: ".bgal { display: flex; gap: 6px; }" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "2d6b44e119a75c820f31e9a44b70c12d8e5f9931",
      short: "2d6b44e",
      subject: "feat(ticketmap): add docked bottom-sheet inspector & focus-scoped keys",
      author: "worker-2a",
      email: "worker@werkschaft.dev",
      age: "1h",
      date: "2026-11-28 10:02",
      col: 1,
      color: 4,
      parents: ["93a1f0e88d42"],
      refs: [
        { kind: "branch", name: "impl/develop/gallery-ticketmap", text: "gallery-ticketmap", impl: true, merged: true }
      ],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 1, to: 0, color: 4 }
      ],
      body: "## AI Context\n- Replaced 340px right sidebar with collapsible bottom sheet when docked inside #bgal.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-ticketmap.js",
          status: "M", add: 112, del: 34,
          hunks: [
            {
              header: "@@ -410,4 +410,7 @@", old: 410, new: 410,
              lines: [
                { k: "+", t: [{ c: "k", s: "if" }, { s: " (docked) " }, { c: "nf", s: "openBottomSheet" }, { s: "(ticket);" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "93a1f0e88d42c19b5e73a01f44b2c8d19e60a477",
      short: "93a1f0e",
      subject: "fix(rail): keep #rail-companion-slot fixed at 0px vertical jitter",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "2h",
      date: "2026-11-28 09:20",
      col: 0,
      color: 0,
      parents: ["6e41c9b22d81"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Moved unregistered workspace disclosure inside .rail-session-panel.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail.css",
          status: "M", add: 18, del: 6,
          hunks: [
            {
              header: "@@ -18,4 +18,6 @@", old: 18, new: 18,
              lines: [
                { k: "+", t: [{ s: ".rail-companion-slot { flex: 0 0 74px; }" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "6e41c9b22d817f53a20c8d94b1e6a3c70f52d118",
      short: "6e41c9b",
      subject: "refactor(shell): settle Variant D per-session top & global bottom bar",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "3h",
      date: "2026-11-28 08:45",
      col: 0,
      color: 0,
      parents: ["1b9a72d44e10"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Separated per-session top chrome from global bottom bar.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-shell.css",
          status: "M", add: 85, del: 41,
          hunks: [
            {
              header: "@@ -12,4 +12,6 @@", old: 12, new: 12,
              lines: [
                { k: "+", t: [{ s: ".shell-top { grid-template-columns: minmax(0,1fr) auto minmax(0,1fr); }" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "1b9a72d44e109c3b8e64f2a71d05c9a38b71e205",
      short: "1b9a72d",
      subject: "test(git): add SVG lane topology regression coverage for multi-parent merges",
      author: "worker-qa",
      email: "qa@werkschaft.dev",
      age: "4h",
      date: "2026-11-28 07:50",
      col: 0,
      color: 0,
      parents: ["d04f88a12c7e"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Verified lane coloring continuity across 2-page graph appends.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/gitlog_test.go",
          status: "M", add: 74, del: 4,
          hunks: [
            {
              header: "@@ -90,3 +90,7 @@", old: 90, new: 90,
              lines: [
                { k: "+", t: [{ c: "k", s: "func" }, { s: " " }, { c: "nf", s: "TestLaneContinuityAcrossPages" }, { s: "(t *testing.T) {" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "d04f88a12c7e5b91a40d3e8f61c2b9a47e50f813",
      short: "d04f88a",
      subject: "chore(ws): bump plugin runtime manifest to v0.53.2",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "5h",
      date: "2026-11-28 06:40",
      col: 0,
      color: 0,
      parents: ["4c72b9df18a6"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Patch release alignment across ws and wsflow manifests.",
      files: [
        {
          path: "agents-plugin/runtime.json",
          status: "M", add: 1, del: 1,
          hunks: [
            {
              header: "@@ -2,3 +2,3 @@", old: 2, new: 2,
              lines: [
                { k: "-", t: [{ s: `  "version": "0.53.1"` }] },
                { k: "+", t: [{ s: `  "version": "0.53.2"` }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "4c72b9df18a63e90c2b4f71a8d5e03b29c41a772",
      short: "4c72b9d",
      subject: "merge(develop): review sweep pass — shell & rail stabilization",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "1d",
      date: "2026-11-27 19:22",
      col: 0,
      color: 0,
      frontier: true,
      parents: ["a78e12c09b31", "3f90b1d77c24"],
      refs: [],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 0, to: 3, color: 3 }
      ],
      body: "## AI Context\n- Last clearing review watermark frontier (verdict: pass).\n- All 4 review partitions (correctness, fit, test, style) passed with 0 Critical findings.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard.css",
          status: "M", add: 46, del: 19,
          hunks: [
            {
              header: "@@ -210,5 +210,9 @@", old: 210, new: 210,
              lines: [
                { k: " ", t: [{ c: "c", s: "/* Review Frontier pass @ 4c72b9d */" }] },
                { k: "+", t: [{ s: ".gcard-hd { background: color-mix(in srgb, var(--wb-muted) 64%, var(--wb-card)); }" }] }
              ]
            }
          ]
        },
        {
          path: "agents-plugin-tool/internal/wsdashboard/review_ledger.go",
          status: "M", add: 31, del: 4,
          hunks: [
            {
              header: "@@ -44,4 +44,8 @@", old: 44, new: 44,
              lines: [
                { k: "+", t: [{ c: "k", s: "return" }, { s: " ReviewFrontier{Head: " }, { c: "s", s: `"4c72b9d"` }, { s: ", Verdict: " }, { c: "s", s: `"pass"` }, { s: "}" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "3f90b1d77c248e5a1b62d9f30c47a1e85b29c601",
      short: "3f90b1d",
      subject: "release(main): v0.53.1 dashboard & MCP compatibility release",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "2d",
      date: "2026-11-26 22:10",
      col: 3,
      color: 3,
      parents: ["a78e12c09b31"],
      refs: [
        { kind: "branch", name: "main", text: "main", upstream: "origin/main" }
      ],
      down: [
        { from: 0, to: 0, color: 0 },
        { from: 3, to: 0, color: 3 }
      ],
      body: "## AI Context\n- Promoted verified develop range to main after review gate cleared.",
      files: [
        {
          path: "agents-plugin/runtime.json",
          status: "M", add: 2, del: 2,
          hunks: [
            {
              header: "@@ -1,4 +1,4 @@", old: 1, new: 1,
              lines: [
                { k: "-", t: [{ s: `  "version": "0.53.0"` }] },
                { k: "+", t: [{ s: `  "version": "0.53.1"` }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "a78e12c09b314d6f82e0a5c19b47f3d28e61c009",
      short: "a78e12c",
      subject: "feat(mcp): add ws/review.marker frontier reader with JSON mode",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "2d",
      date: "2026-11-26 18:05",
      col: 0,
      color: 0,
      parents: ["7c20b4e91f88"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Exposed read-only frontier lookup so dashboard and ship gate share one ledger source.",
      files: [
        {
          path: "agents-plugin-tool/internal/mcp/review_tools.go",
          status: "M", add: 52, del: 8,
          hunks: [
            {
              header: "@@ -18,4 +18,7 @@", old: 18, new: 18,
              lines: [
                { k: "+", t: [{ c: "k", s: "func" }, { s: " " }, { c: "nf", s: "handleReviewMarker" }, { s: "(req MarkerRequest) MarkerResponse {" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "7c20b4e91f883a2d6c50e1b94f72a8d31e09b512",
      short: "7c20b4e",
      subject: "fix(dashboard): clamp dark mode scrollbars & hover contrast",
      author: "worker-ui",
      email: "ui@werkschaft.dev",
      age: "3d",
      date: "2026-11-25 16:40",
      col: 0,
      color: 0,
      parents: ["50a8d1f64c23"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Tuned standard scrollbar-color tokens for Windows/macOS parity.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard.css",
          status: "M", add: 8, del: 4,
          hunks: [
            {
              header: "@@ -96,3 +96,5 @@", old: 96, new: 96,
              lines: [
                { k: "+", t: [{ s: "* { scrollbar-width: thin; }" }] }
              ]
            }
          ]
        }
      ]
    },
    {
      oid: "50a8d1f64c239e7b14d0c8a52f31e9b70c44a820",
      short: "50a8d1f",
      subject: "perf(git): stream unified diffs lazily per file section in #gwin",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "3d",
      date: "2026-11-25 11:12",
      col: 0,
      color: 0,
      parents: ["2b19e7c83a50"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Uses IntersectionObserver with 200px rootMargin; files >400 lines gate behind Load diff.",
      files: [
        {
          path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-git.js",
          status: "M", add: 88, del: 22,
          hunks: [
            {
              header: "@@ -890,3 +890,6 @@", old: 890, new: 890,
              lines: [
                { k: "+", t: [{ c: "k", s: "var" }, { s: " GW_MANUAL = " }, { c: "m", s: "400" }, { s: ";" }] }
              ]
            }
          ]
        }
      ]
    }
  ];

  const COMMITS_PAGE_2 = [
    {
      oid: "2b19e7c83a504f12d9c8e6b40a71f3c25d88e104",
      short: "2b19e7c",
      subject: "feat(worktrees): add lease holder & profile metadata to pool JSON",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "4d",
      date: "2026-11-24 17:30",
      col: 0,
      color: 0,
      parents: ["9e44a2b71c03"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Enriched pool rows with holder session label and profile classification.",
      files: []
    },
    {
      oid: "9e44a2b71c038d51f6e2a9c40b83d1e72f56a019",
      short: "9e44a2b",
      subject: "refactor(board): move pinned gallery viewport to transform-only slides",
      author: "worker-ui",
      email: "ui@werkschaft.dev",
      age: "4d",
      date: "2026-11-24 14:15",
      col: 0,
      color: 0,
      parents: ["11c7f8e32a90"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Preserves docked iframe/terminal state without unmounting.",
      files: []
    },
    {
      oid: "11c7f8e32a905b41d8e3c6a72f19b0e48c32d771",
      short: "11c7f8e",
      subject: "fix(hold-confirm): cancel destructive hold cleanly on tab blur",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "5d",
      date: "2026-11-23 20:02",
      col: 0,
      color: 0,
      parents: ["f82c10a49b65"],
      refs: [],
      down: [{ from: 0, to: 0, color: 0 }],
      body: "## AI Context\n- Added visibilitychange cancellation listener in hold-confirm.js.",
      files: []
    },
    {
      oid: "f82c10a49b653e28d1a7c9f40b62e8d13a54c902",
      short: "f82c10a",
      subject: "docs(style): codify Material tonal surface ladder in dashboard-style.md",
      author: "kang-sw",
      email: "kang-sw@werkschaft.dev",
      age: "6d",
      date: "2026-11-22 15:48",
      col: 0,
      color: 0,
      parents: [],
      refs: [],
      down: [],
      body: "## AI Context\n- Established dark/light token reference and anti-patterns.",
      files: []
    }
  ];

  // --- Working Tree Changes Fixture (#gpanel) -----------------------------
  const WORKTREE_CHANGES = {
    staged: [
      {
        path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-git-page.css",
        status: "A", add: 96, del: 0,
        hunks: [
          {
            header: "@@ -0,0 +1,12 @@", old: 1, new: 1,
            lines: [
              { k: "+", t: [{ c: "c", s: "/* Standalone Git Page inside Always-Open #bgal */" }] },
              { k: "+", t: [{ s: ".git-strip { " }, { c: "kt", s: "display" }, { s: ": flex; " }, { c: "kt", s: "justify-content" }, { s: ": space-between; }" }] },
              { k: "+", t: [{ s: ".review-pill { " }, { c: "kt", s: "border-radius" }, { s: ": " }, { c: "m", s: "999px" }, { s: "; " }, { c: "kt", s: "cursor" }, { s: ": pointer; }" }] },
              { k: "+", t: [{ s: ".wt-scan-btn { " }, { c: "kt", s: "min-height" }, { s: ": " }, { c: "m", s: "42px" }, { s: "; " }, { c: "kt", s: "width" }, { s: ": " }, { c: "m", s: "100%" }, { s: "; }" }] }
            ]
          }
        ]
      },
      {
        path: "agents-plugin-tool/internal/wsdashboard/git_review_summary.go",
        status: "M", add: 41, del: 12,
        hunks: [
          {
            header: "@@ -28,7 +28,11 @@ func BuildReviewSummary(repo string) (*ReviewSummary, error)", old: 28, new: 28,
            lines: [
              { k: " ", t: [{ s: "  frontier, err := ledger.ReadFrontier(repo)" }] },
              { k: "-", t: [{ s: "  " }, { c: "k", s: "return" }, { s: " &ReviewSummary{Head: frontier.Head}, nil" }] },
              { k: "+", t: [{ s: "  merges, add, del := countRangeStats(repo, frontier.Head, " }, { c: "s", s: `"HEAD"` }, { s: ")" }] },
              { k: "+", t: [{ s: "  " }, { c: "k", s: "return" }, { s: " &ReviewSummary{Head: frontier.Head, Verdict: frontier.Verdict, Merges: merges, Add: add, Del: del}, nil" }] },
              { k: " ", t: [{ s: "}" }] }
            ]
          }
        ]
      }
    ],
    unstaged: [
      {
        path: "agents-plugin-tool/internal/wsdashboard/assets/dashboard-git.js",
        status: "M", add: 34, del: 18,
        hunks: [
          {
            header: "@@ -442,6 +442,10 @@ function glDraw()", old: 442, new: 442,
            lines: [
              { k: " ", t: [{ s: "  " }, { c: "k", s: "if" }, { s: " (c.oid.startsWith(reviewFrontier)) {" }] },
              { k: "+", t: [{ s: "    row.classList.add(" }, { c: "s", s: "'is-frontier'" }, { s: ");" }] },
              { k: "+", t: [{ s: "    sub.appendChild(" }, { c: "nf", s: "mkFrontierBadge" }, { s: "());" }] },
              { k: " ", t: [{ s: "  }" }] }
            ]
          }
        ]
      },
      {
        path: "agents-plugin-tool/internal/wsdashboard/worktrees.go",
        status: "M", add: 13, del: 12,
        manualNotice: "대용량 변경 미리보기 테스트 (클릭하여 지연 Diff 로드)",
        hunks: [
          {
            header: "@@ -104,5 +104,7 @@ func (s *Server) listWorktreePool(scan bool)", old: 104, new: 104,
            lines: [
              { k: "-", t: [{ s: "  " }, { c: "k", s: "if" }, { s: " !pool.Sized { go s.sizePoolAsync() }" }] },
              { k: "+", t: [{ c: "c", s: "  // Only scan sizes on explicit manual refresh (?scan=1)" }] },
              { k: "+", t: [{ s: "  " }, { c: "k", s: "if" }, { s: " scan { s.scanPoolSizes(ctx, pool) }" }] }
            ]
          }
        ]
      }
    ],
    untracked: [
      {
        path: "agents-plugin-tool/internal/wsdashboard/assets/git-frontier-jump.test.js",
        status: "?", add: 0, del: 0,
        hunks: [
          {
            header: "@@ -0,0 +1,6 @@", old: 1, new: 1,
            lines: [
              { k: "+", t: [{ c: "c", s: "// Verifies Review Ledger click switches Narrow tab -> graph and highlights 4c72b9d" }] },
              { k: "+", t: [{ c: "nf", s: "test" }, { s: "(" }, { c: "s", s: "'review pill jumps to frontier'" }, { s: ", () => { " }, { c: "nf", s: "expect" }, { s: "(activeTab).toBe(" }, { c: "s", s: "'graph'" }, { s: "); });" }] }
            ]
          }
        ]
      }
    ]
  };

  // --- Pooled Worktrees Fixture ((1729)=a) --------------------------------
  const WORKTREES = [
    {
      id: "wt-gallery-git",
      slug: "wt-gallery-git",
      path: "/Users/kang-sw/.ws-pool/devenv/wt-gallery-git",
      profile: "full",
      branch: "impl/develop/gallery-git-page",
      holder: "기능 2 (#42)",
      state: "active",
      age: "14m",
      scannedSize: "1.42 GB",
      heavy: true
    },
    {
      id: "wt-account-slots",
      slug: "wt-account-slots",
      path: "/Users/kang-sw/.ws-pool/devenv/wt-account-slots",
      profile: "full",
      branch: "impl/develop/account-slots",
      holder: "기능 1 (#41)",
      state: "active",
      age: "38m",
      scannedSize: "680 MB",
      heavy: false
    },
    {
      id: "wt-housekeep-01",
      slug: "wt-housekeep-01",
      path: "/Users/kang-sw/.ws-pool/devenv/wt-housekeep-01",
      profile: "housekeeping",
      branch: "(detached @ e91c40b)",
      holder: "",
      state: "idle",
      age: "3h",
      scannedSize: "12 MB",
      heavy: false
    },
    {
      id: "wt-legacy-drawer",
      slug: "wt-legacy-drawer",
      path: "/Users/kang-sw/.ws-pool/devenv/wt-legacy-drawer",
      profile: "housekeeping",
      branch: "impl/develop/old-drawer-sheet",
      holder: "",
      state: "stale",
      age: "9d",
      scannedSize: "18.4 MB",
      heavy: false
    }
  ];

  // --- State --------------------------------------------------------------
  const state = {
    preset: "wide",
    theme: "dark",
    bgalWidth: 900,
    activePage: "git",
    narrowTab: "graph",
    commits: COMMITS_PAGE_1.slice(),
    hasMore: true,
    paginatedFrontierDemo: false,
    gitBusy: false,
    ahead: 1,
    behind: 0,
    collapsedGroups: { staged: false, unstaged: false, untracked: false },
    wtState: "light", // "light" | "scanning" | "scanned"
    wtSizes: {},      // id -> string | "scanning"
    wtScanTimerIds: [],
    selectedCommitOid: "",
    toastTimer: null
  };

  // --- DOM Helpers --------------------------------------------------------
  const $ = (id) => document.getElementById(id);
  const root = document.documentElement;
  const bgal = $("bgal");
  const glrows = $("glrows");
  const glscroll = $("glscroll");
  const glcount = $("glcount");
  const gfiles = $("gfiles");
  const wtlist = $("wtlist");
  const wtScanBtn = $("wt-scan-btn");
  const wtScanProg = $("wt-scan-prog");
  const wtScanTitle = $("wt-scan-title");
  const wtScanSub = $("wt-scan-sub");
  const wtScanTag = $("wt-scan-tag");
  const wtModePill = $("wt-mode-pill");
  const wtModeTxt = $("wt-mode-txt");
  const wtNote = $("wtnote");
  const wtClean = $("wtclean");
  const gitRegions = $("git-regions");
  const gitFeedback = $("git-feedback");
  const gitToast = $("git-toast");
  const gltip = $("gltip");
  const galPop = $("gal-pop");
  const gwinBack = $("gwin-back");
  const gwin = $("gwin");

  function mk(tag, cls, text) {
    const el = document.createElement(tag);
    if (cls) el.className = cls;
    if (text != null) el.textContent = text;
    return el;
  }

  function svgEl(tag, attrs) {
    const el = document.createElementNS(SVGNS, tag);
    for (const k in attrs) el.setAttribute(k, attrs[k]);
    return el;
  }

  function iconUse(name, cls) {
    const s = svgEl("svg", { class: cls || "i", "aria-hidden": "true" });
    s.appendChild(svgEl("use", { href: "#i-" + name }));
    return s;
  }

  function showToast(msg, duration) {
    clearTimeout(state.toastTimer);
    gitToast.textContent = msg;
    gitToast.hidden = false;
    state.toastTimer = setTimeout(() => {
      gitToast.hidden = true;
    }, duration || 3200);
  }

  function showFeedback(msg, kind) {
    gitFeedback.textContent = msg;
    gitFeedback.className = "git-feedback" + (kind === "info" ? " is-info" : "");
    gitFeedback.hidden = !msg;
  }

  // --- Commit Graph Rendering (#glog) -------------------------------------
  function glX(col) { return GL_X0 + col * GL_DX; }
  function glY(i) { return i * GL_ROW + GL_ROW / 2; }
  function glLane(c) { return "var(--gl-" + (c % 8) + ")"; }

  function buildRefPills(c) {
    const out = [];
    (c.refs || []).forEach((r) => {
      const p = mk("span", "rf" + (r.head ? " head" : "") + (r.kind === "upstream" ? " remote" : "") + (r.merged ? " merged" : ""));
      p.style.setProperty("--lc", glLane(c.color));
      if (r.head) p.appendChild(mk("span", "hd", "HEAD"));
      if (r.impl) {
        const ic = mk("span", "gh-impl");
        ic.appendChild(iconUse("hammer", "i"));
        p.appendChild(ic);
      } else {
        p.appendChild(iconUse("branch", "i"));
      }
      p.appendChild(mk("span", "t", r.text || r.name));
      if (r.worktree) {
        const wt = mk("span", "wt");
        wt.appendChild(iconUse("folder", "i"));
        p.appendChild(wt);
      }
      if (r.upstream) {
        p.appendChild(mk("span", "ups", r.upstream.split("/")[0]));
      }
      out.push(p);
    });
    if (c.frontier) {
      const f = mk("span", "rf-frontier");
      f.appendChild(iconUse("shield", "i"));
      f.appendChild(document.createTextNode("Review Frontier"));
      out.push(f);
    }
    return out;
  }

  function renderGraph() {
    const cs = state.commits;
    const n = cs.length;
    const keepScroll = glscroll.scrollTop;
    let maxCol = 0;
    cs.forEach((c) => {
      if (c.col > maxCol) maxCol = c.col;
      (c.down || []).forEach((s) => {
        if (s.from > maxCol) maxCol = s.from;
        if (s.to > maxCol) maxCol = s.to;
      });
    });

    const w = glX(maxCol) + 14;
    const H = n * GL_ROW;
    glscroll.style.setProperty("--gw", Math.max(w, 54) + "px");
    glcount.textContent = n + (state.hasMore ? "+" : "");

    glrows.textContent = "";
    const svg = svgEl("svg", {
      class: "glsvg",
      width: w,
      height: H + (state.hasMore ? GL_MORE : 4),
      "aria-hidden": "true"
    });
    const gLines = svgEl("g", {});
    const gDots = svgEl("g", {});
    const frag = document.createDocumentFragment();

    cs.forEach((c, i) => {
      const isLast = i === n - 1;
      (c.down || []).forEach((s) => {
        const x1 = glX(s.from);
        const y1 = glY(i);
        const x2 = glX(s.to);
        const y2 = isLast ? H + GL_MORE * 0.55 : glY(i + 1);
        const mid = (y1 + y2) / 2;
        const d = x1 === x2
          ? `M${x1} ${y1}V${y2}`
          : `M${x1} ${y1}C${x1} ${mid} ${x2} ${mid} ${x2} ${y2}`;
        const attrs = { d, stroke: glLane(s.color) };
        if (isLast && state.hasMore) attrs.class = "cont";
        gLines.appendChild(svgEl("path", attrs));
      });

      const x = glX(c.col);
      const y = glY(i);
      const lc = glLane(c.color);
      const isMerge = c.parents && c.parents.length > 1;
      const isHead = (c.refs || []).some((r) => r.head);

      if (isHead) gDots.appendChild(svgEl("circle", { class: "head", cx: x, cy: y, r: 8 }));
      if (c.frontier) gDots.appendChild(svgEl("circle", { class: "frontier-ring", cx: x, cy: y, r: 8.5 }));
      if (isMerge) gDots.appendChild(svgEl("circle", { class: "mg", cx: x, cy: y, r: 4.3, stroke: lc }));
      else gDots.appendChild(svgEl("circle", { class: "dot", cx: x, cy: y, r: 4.8, fill: lc }));

      const row = mk(
        "div",
        "glr" +
          (isMerge ? " mg" : "") +
          (c.frontier ? " is-frontier" : "") +
          (c.oid === state.selectedCommitOid && !gwinBack.hidden ? " sel" : "")
      );
      row.tabIndex = 0;
      row.setAttribute("data-i", String(i));
      row.setAttribute("data-sha", c.short);
      row.setAttribute("role", "button");
      row.setAttribute("aria-label", `${c.short} ${c.subject}`);

      row.appendChild(mk("span")); // Graph lane column spacer
      const sub = mk("span", "gls");
      buildRefPills(c).forEach((p) => sub.appendChild(p));
      sub.appendChild(mk("span", "glsub", c.subject));
      row.appendChild(sub);
      row.appendChild(mk("span", "gla", c.author));
      row.appendChild(mk("span", "glt", c.age));
      row.appendChild(mk("span", "glh", c.short));
      frag.appendChild(row);
    });

    if (state.hasMore) {
      const more = mk("div", "glmore");
      const btn = mk("button", "glmore-b", `더 보기 (+${COMMITS_PAGE_2.length} 커밋)`);
      btn.type = "button";
      more.appendChild(btn);
      more.appendChild(mk("span", "", "이전 히스토리 페이징"));
      frag.appendChild(more);
    }

    svg.appendChild(gLines);
    svg.appendChild(gDots);
    frag.appendChild(svg);
    glrows.appendChild(frag);
    glscroll.scrollTop = keepScroll;
  }

  function loadMoreCommits() {
    if (!state.hasMore) return false;
    state.commits = state.commits.concat(COMMITS_PAGE_2);
    state.hasMore = false;
    renderGraph();
    return true;
  }

  // --- Key Interaction ((1726)=a): Jump to Review Frontier (4c72b9d) ------
  function jumpToReviewFrontier() {
    // 1. Switch to Git page if viewing a stub page
    if (state.activePage !== "git") setGalleryPage("git");

    // 2. Auto-switch to Graph tab in Narrow mode ((1728)=a)
    const switchedTab = state.narrowTab !== "graph";
    if (switchedTab) setNarrowTab("graph");

    // 3. Ensure frontier commit is loaded (simulates auto-pagination if ever needed)
    let loadedViaPage = false;
    let row = glrows.querySelector(`.glr[data-sha="${FRONTIER_SHA}"]`);
    if (!row && state.hasMore) {
      loadMoreCommits();
      loadedViaPage = true;
      row = glrows.querySelector(`.glr[data-sha="${FRONTIER_SHA}"]`);
    }
    if (!row) return;

    // 4. Smooth scroll into center + brief spotlight pulse highlight
    requestAnimationFrame(() => {
      const rowTop = row.offsetTop;
      const targetScroll = Math.max(0, rowTop - glscroll.clientHeight / 2 + GL_ROW / 2);
      glscroll.scrollTo({ top: targetScroll, behavior: "smooth" });

      row.classList.remove("is-spotlight");
      void row.offsetWidth;
      row.classList.add("is-spotlight");
      setTimeout(() => row.classList.remove("is-spotlight"), 1900);
    });

    const isNarrow = bgal.getBoundingClientRect().width <= 719;
    let msg = `Review Frontier (${FRONTIER_SHA} · pass)로 이동했습니다`;
    if (loadedViaPage) msg = `페이지 2 자동 로드 후 ${msg}`;
    else if (isNarrow && switchedTab) msg = `그래프 탭 전환 · ${msg}`;
    showToast(msg, 3200);
  }

  // --- Working Tree Changes Rendering (#gpanel) ---------------------------
  function renderWorkingTree() {
    gfiles.textContent = "";
    const defs = [
      { key: "staged", label: "Staged", hint: "커밋 준비됨" },
      { key: "unstaged", label: "Unstaged", hint: "작업 중" },
      { key: "untracked", label: "Untracked", hint: "새 파일" }
    ];
    let total = 0;
    let totalAdd = 0;
    let totalDel = 0;

    defs.forEach((d) => {
      const list = WORKTREE_CHANGES[d.key] || [];
      total += list.length;
      if (!list.length) return;

      const sec = mk("div", "gsec" + (state.collapsedGroups[d.key] ? " is-col" : ""));
      const head = mk("button", "ggrp");
      head.type = "button";
      head.setAttribute("aria-expanded", String(!state.collapsedGroups[d.key]));
      head.appendChild(iconUse("chev", "ic-chev"));
      head.appendChild(mk("span", "", d.label));
      head.appendChild(mk("span", "chip", String(list.length)));
      head.appendChild(mk("span", "ggrp-hint", d.hint));
      head.addEventListener("click", () => {
        state.collapsedGroups[d.key] = !state.collapsedGroups[d.key];
        renderWorkingTree();
      });
      sec.appendChild(head);

      const rows = mk("div", "gsec-rows");
      list.forEach((f, idx) => {
        totalAdd += f.add || 0;
        totalDel += f.del || 0;
        const row = mk("button", "gf");
        row.type = "button";
        row.setAttribute("data-side", d.key);
        row.setAttribute("data-i", String(idx));
        row.title = f.path;

        const stCls = "gst st-" + (f.status === "?" ? "q" : f.status);
        row.appendChild(mk("b", stCls, f.status));

        const p = mk("span", "gp");
        const slash = f.path.lastIndexOf("/");
        if (slash >= 0) p.appendChild(mk("span", "gdir", f.path.slice(0, slash + 1)));
        p.appendChild(mk("span", "gname", f.path.slice(slash + 1)));
        row.appendChild(p);

        const gd = mk("span", "gd");
        if (f.status === "?") {
          gd.appendChild(mk("i", "untr", "new"));
        } else {
          if (f.add) gd.appendChild(mk("i", "add", "+" + f.add));
          if (f.del) gd.appendChild(mk("i", "del", "−" + f.del));
        }
        row.appendChild(gd);
        rows.appendChild(row);
      });

      sec.appendChild(rows);
      gfiles.appendChild(sec);
    });

    $("gdiff-cnt").textContent = String(total);
    $("tab-diff-cnt").textContent = String(total);
    $("gpanel-sum").innerHTML = `<em class="add">+${totalAdd}</em><em class="del">−${totalDel}</em>`;
  }

  // --- Pooled Worktrees Rendering & Manual WalkDir Scan ((1729)=a) --------
  function renderWorktrees() {
    wtlist.textContent = "";
    const activeCount = WORKTREES.filter((w) => w.state === "active").length;
    $("wtcount").textContent = `${activeCount}/${WORKTREES.length}`;
    $("tab-wt-cnt").textContent = String(WORKTREES.length);

    WORKTREES.forEach((w) => {
      const cls = "wtrow" + (w.state === "active" ? " on" : "") + (w.state === "stale" ? " stale" : "");
      const row = mk("div", cls);
      row.title = `${w.path}\nprofile: ${w.profile} · branch: ${w.branch}`;

      row.appendChild(mk("span", "wtdot"));
      row.appendChild(mk("b", "wtname", w.slug));
      row.appendChild(mk("span", "wtprof" + (w.profile === "full" ? " full" : ""), w.profile));

      const szVal = state.wtSizes[w.id];
      const sz = mk("span", "wtsize");
      if (szVal === "scanning") {
        sz.textContent = "스캔 중…";
        sz.classList.add("is-scanning");
      } else if (szVal) {
        sz.textContent = szVal;
        sz.classList.add("is-ready");
        if (w.heavy) sz.classList.add("is-heavy");
      } else {
        sz.textContent = "—";
        sz.title = "용량 미스캔 — 상단 새로 고침 버튼으로 전체 스캔";
      }
      row.appendChild(sz);

      const meta = mk("div", "wtmeta");
      meta.appendChild(mk("span", "wt-st-chip " + w.state, w.state));
      if (w.holder) meta.appendChild(mk("span", "wtwho", w.holder));
      meta.appendChild(mk("span", "wtbr", w.branch));
      row.appendChild(meta);

      row.appendChild(mk("span", "wtage", w.age));
      wtlist.appendChild(row);
    });

    // Sync header badge + prominent scan button state
    wtModePill.className = "wt-mode-pill";
    wtScanBtn.className = "wt-scan-btn";

    if (state.wtState === "light") {
      wtModePill.classList.add("is-light");
      wtModeTxt.textContent = "경량 목록 · 용량 미스캔";
      wtScanBtn.disabled = false;
      wtScanProg.style.width = "0%";
      wtScanTitle.textContent = "워크트리 새로 고침 (용량 · 상태 전체 스캔)";
      wtScanSub.textContent = "경량 목록은 디스크를 읽지 않습니다 · 클릭 시 WalkDir 스캔 실행";
      wtScanTag.textContent = "WalkDir";
      wtNote.className = "wtnote";
      wtNote.textContent = "용량 스캔 후 stale 워크트리 정리 가능량을 확인할 수 있습니다.";
    } else if (state.wtState === "scanning") {
      wtModePill.classList.add("is-scanning");
      wtModeTxt.textContent = "WalkDir 용량 스캔 중…";
      wtScanBtn.classList.add("is-scanning");
      wtScanBtn.disabled = true;
      const done = WORKTREES.filter((w) => state.wtSizes[w.id] && state.wtSizes[w.id] !== "scanning").length;
      wtScanTitle.textContent = `워크트리 용량 · 상태 전체 스캔 중… (${done}/${WORKTREES.length})`;
      wtScanSub.textContent = "filepath.WalkDir 실행 중 · 완료 시 2분 캐시 저장";
      wtScanTag.textContent = `${Math.round((done / WORKTREES.length) * 100)}%`;
      wtNote.className = "wtnote";
      wtNote.textContent = "각 워크트리 디렉토리 크기를 순차 집계하고 있습니다…";
    } else {
      wtModePill.classList.add("is-scanned");
      wtModeTxt.textContent = "용량 스캔 완료 · 2m 캐시";
      wtScanBtn.classList.add("is-scanned");
      wtScanBtn.disabled = false;
      wtScanProg.style.width = "0%";
      wtScanTitle.textContent = "워크트리 다시 스캔 (합계 2.13 GB · 2m 캐시)";
      wtScanSub.textContent = "stale 1건(18.4 MB) 감지됨 · 클릭하여 즉시 재스캔";
      wtScanTag.textContent = "2.13 GB";
      wtNote.className = "wtnote is-ok";
      wtNote.textContent = "스캔 완료: wt-legacy-drawer (stale · 18.4 MB) 정리 가능";
    }
  }

  function clearScanTimers() {
    state.wtScanTimerIds.forEach((id) => clearTimeout(id));
    state.wtScanTimerIds = [];
  }

  function runWorktreeScan(keepBusyLoop) {
    clearScanTimers();
    state.wtState = "scanning";
    state.wtSizes = {};
    WORKTREES.forEach((w) => { state.wtSizes[w.id] = "scanning"; });
    wtScanProg.style.width = "12%";
    renderWorktrees();

    const delays = [280, 580, 880, 1180];
    WORKTREES.forEach((w, idx) => {
      const tid = setTimeout(() => {
        state.wtSizes[w.id] = w.scannedSize;
        wtScanProg.style.width = `${Math.round(((idx + 1) / WORKTREES.length) * 100)}%`;
        if (idx === WORKTREES.length - 1 && !keepBusyLoop) {
          state.wtState = "scanned";
        }
        renderWorktrees();
      }, delays[idx]);
      state.wtScanTimerIds.push(tid);
    });

    if (keepBusyLoop) {
      // In "Scanning / Busy State" preset, hold mid-scan on 2/4 so reviewers see live scanning visuals clearly
      clearScanTimers();
      state.wtSizes[WORKTREES[0].id] = WORKTREES[0].scannedSize;
      state.wtSizes[WORKTREES[1].id] = WORKTREES[1].scannedSize;
      state.wtSizes[WORKTREES[2].id] = "scanning";
      state.wtSizes[WORKTREES[3].id] = "scanning";
      wtScanProg.style.width = "54%";
      renderWorktrees();
    }
  }

  function resetWorktreeLight() {
    clearScanTimers();
    state.wtState = "light";
    state.wtSizes = {};
    renderWorktrees();
  }

  // --- Remote Git Actions (Fetch / Pull / Push) ---------------------------
  function runGitAction(btn) {
    if (state.gitBusy) return;
    const act = btn.getAttribute("data-git-act");
    const span = btn.querySelector("span");
    const idleLbl = span.textContent;
    const busyLbl = btn.getAttribute("data-busy-lbl");

    state.gitBusy = true;
    document.querySelectorAll(".gact-btn").forEach((b) => { b.disabled = true; });
    btn.classList.add("is-busy");
    span.textContent = busyLbl;
    showFeedback(`${act.toUpperCase()} 실행 중…`, "info");

    setTimeout(() => {
      state.gitBusy = false;
      document.querySelectorAll(".gact-btn").forEach((b) => { b.disabled = false; });
      btn.classList.remove("is-busy");
      span.textContent = idleLbl;

      if (act === "push") {
        state.ahead = 0;
        $("git-ahead").textContent = "↑0";
        showFeedback("origin/develop으로 1개 커밋을 푸시했습니다 (8f31a2c).", "ok");
      } else if (act === "fetch") {
        showFeedback("origin/develop 최신 상태 확인 완료 (변경 없음).", "ok");
      } else {
        showFeedback("이미 최신 상태입니다 (Already up to date).", "ok");
      }
      setTimeout(() => { if (!state.gitBusy) showFeedback(""); }, 3200);
    }, 850);
  }

  // --- Simulated #gwin-back Commit & Working-Tree Diff Modal --------------
  function renderDiffLines(container, file) {
    container.textContent = "";
    const tab = mk("div", "dtab chroma");
    (file.hunks || []).forEach((h) => {
      let o = h.old;
      let n = h.new;
      tab.appendChild(mkDiffRow("hk", "", "", "", [{ s: h.header }]));
      h.lines.forEach((l) => {
        if (l.k === "+") tab.appendChild(mkDiffRow("add", "", n++, "+", l.t));
        else if (l.k === "-") tab.appendChild(mkDiffRow("del", o++, "", "−", l.t));
        else tab.appendChild(mkDiffRow("", o++, n++, "", l.t));
      });
    });
    if (!(file.hunks || []).length) {
      container.appendChild(mk("div", "dnote", "변경된 텍스트 헝크가 없는 메타데이터 커밋입니다."));
    } else {
      container.appendChild(tab);
    }
  }

  function mkDiffRow(cls, o, n, sign, pieces) {
    const row = mk("div", "dl" + (cls ? " " + cls : ""));
    row.appendChild(mk("span", "no", String(o)));
    row.appendChild(mk("span", "no", String(n)));
    row.appendChild(mk("span", "sg", sign));
    const cd = mk("span", "cd");
    (pieces || []).forEach((p) => {
      if (!p.c) cd.appendChild(document.createTextNode(p.s));
      else cd.appendChild(mk("span", "t-" + p.c, p.s));
    });
    row.appendChild(cd);
    return row;
  }

  function populateModalFiles(files, focusIdx) {
    const gwFiles = $("gw-files");
    const gwDiff = $("gw-diff");
    gwFiles.textContent = "";
    gwDiff.textContent = "";

    let addSum = 0;
    let delSum = 0;
    files.forEach((f) => { addSum += f.add || 0; delSum += f.del || 0; });

    const sum = mk("div", "gw-fsum");
    sum.appendChild(mk("span", "", `변경 파일 ${files.length}개`));
    const gd = mk("span", "gd");
    if (addSum) gd.appendChild(mk("i", "add", "+" + addSum));
    if (delSum) gd.appendChild(mk("i", "del", "−" + delSum));
    sum.appendChild(gd);
    gwFiles.appendChild(sum);

    files.forEach((f, idx) => {
      // Sidebar file row
      const row = mk("button", "gf gwf" + (idx === (focusIdx >= 0 ? focusIdx : 0) ? " on" : ""));
      row.type = "button";
      row.appendChild(mk("b", "gst st-" + (f.status === "?" ? "q" : f.status), f.status));
      const p = mk("span", "gp");
      const slash = f.path.lastIndexOf("/");
      if (slash >= 0) p.appendChild(mk("span", "gdir", f.path.slice(0, slash + 1)));
      p.appendChild(mk("span", "gname", f.path.slice(slash + 1)));
      row.appendChild(p);
      const fd = mk("span", "gd");
      if (f.add) fd.appendChild(mk("i", "add", "+" + f.add));
      if (f.del) fd.appendChild(mk("i", "del", "−" + f.del));
      row.appendChild(fd);
      row.addEventListener("click", () => {
        gwFiles.querySelectorAll(".gwf").forEach((el, j) => el.classList.toggle("on", j === idx));
        const sec = gwDiff.querySelector(`.dfile[data-fi="${idx}"]`);
        if (sec) {
          sec.classList.remove("fold");
          sec.scrollIntoView({ behavior: "smooth", block: "start" });
        }
      });
      gwFiles.appendChild(row);

      // Diff section card
      const sec = mk("section", "dfile");
      sec.setAttribute("data-fi", String(idx));
      const hd = mk("header", "dfh");
      const fold = mk("button", "ia fold-b");
      fold.type = "button";
      fold.title = "접기/펼치기";
      fold.appendChild(iconUse("chev", "ic-chev"));
      fold.addEventListener("click", () => sec.classList.toggle("fold"));
      hd.appendChild(fold);
      hd.appendChild(mk("b", "gst st-" + (f.status === "?" ? "q" : f.status), f.status));
      const hp = mk("span", "gp");
      if (slash >= 0) hp.appendChild(mk("span", "gdir", f.path.slice(0, slash + 1)));
      hp.appendChild(mk("span", "gname", f.path.slice(slash + 1)));
      hd.appendChild(hp);
      const hgd = mk("span", "gd");
      if (f.add) hgd.appendChild(mk("i", "add", "+" + f.add));
      if (f.del) hgd.appendChild(mk("i", "del", "−" + f.del));
      hd.appendChild(hgd);
      sec.appendChild(hd);

      const body = mk("div", "dfb");
      if (f.manualNotice && idx !== focusIdx) {
        const note = mk("div", "dnote");
        note.appendChild(mk("span", "", f.manualNotice));
        const lb = mk("button", "dnote-btn", "Diff 불러오기");
        lb.type = "button";
        lb.addEventListener("click", () => renderDiffLines(body, f));
        note.appendChild(lb);
        body.appendChild(note);
      } else {
        renderDiffLines(body, f);
      }
      sec.appendChild(body);
      gwDiff.appendChild(sec);
    });

    if (focusIdx > 0) {
      requestAnimationFrame(() => {
        const target = gwDiff.querySelector(`.dfile[data-fi="${focusIdx}"]`);
        if (target) target.scrollIntoView({ block: "start" });
      });
    } else {
      gwDiff.scrollTop = 0;
    }
  }

  function openCommitModal(commit) {
    state.selectedCommitOid = commit.oid;
    $("gwsubj").textContent = commit.subject;
    const copyBtn = $("gw-copy");
    copyBtn.hidden = false;
    copyBtn.setAttribute("data-oid", commit.oid);

    const chips = $("gw-chips");
    chips.textContent = "";
    chips.appendChild(mk("code", "hash", commit.oid.slice(0, 12)));
    buildRefPills(commit).forEach((p) => chips.appendChild(p));
    if (commit.parents.length > 1) chips.appendChild(mk("span", "gw-note", "Merge commit"));

    const meta = $("gw-meta");
    meta.textContent = "";
    meta.appendChild(mk("dt", "", "Author"));
    const ddA = mk("dd");
    ddA.innerHTML = `${commit.author} <span class="em">&lt;${commit.email}&gt; · ${commit.age} (${commit.date})</span>`;
    meta.appendChild(ddA);

    meta.appendChild(mk("dt", "", "Parents"));
    const ddP = mk("dd");
    if (!commit.parents.length) {
      ddP.appendChild(mk("span", "em", "root commit"));
    } else {
      commit.parents.forEach((p, idx) => {
        if (idx) ddP.appendChild(document.createTextNode(" "));
        const pb = mk("button", "gw-par", p.slice(0, 7));
        pb.type = "button";
        pb.addEventListener("click", () => {
          const found = state.commits.concat(COMMITS_PAGE_2).find((x) => x.oid.startsWith(p.slice(0, 7)));
          if (found) openCommitModal(found);
        });
        ddP.appendChild(pb);
      });
    }
    meta.appendChild(ddP);

    const msg = $("gw-msg");
    msg.textContent = commit.body || "";
    msg.hidden = !commit.body;

    populateModalFiles(commit.files || [], -1);
    gwinBack.hidden = false;
    renderGraph();
    gwin.focus();
  }

  function openWorktreeDiffModal(side, idx) {
    state.selectedCommitOid = "";
    $("gwsubj").textContent = "워킹 트리 변경 사항 (Working Tree Diff)";
    $("gw-copy").hidden = true;

    const chips = $("gw-chips");
    chips.textContent = "";
    chips.appendChild(mk("code", "hash", "develop"));
    chips.appendChild(mk("span", "gw-note", `선택 그룹: ${side}`));

    const meta = $("gw-meta");
    meta.textContent = "";
    meta.appendChild(mk("dt", "", "Branch"));
    meta.appendChild(mk("dd", "", `develop → origin/develop (↑${state.ahead} ↓${state.behind})`));
    $("gw-msg").hidden = true;

    const all = [];
    let focus = 0;
    let cursor = 0;
    ["staged", "unstaged", "untracked"].forEach((k) => {
      (WORKTREE_CHANGES[k] || []).forEach((f, i) => {
        if (k === side && i === idx) focus = cursor;
        all.push(f);
        cursor++;
      });
    });

    populateModalFiles(all, focus);
    gwinBack.hidden = false;
    gwin.focus();
  }

  function closeModal() {
    if (gwinBack.hidden) return;
    gwinBack.hidden = true;
    state.selectedCommitOid = "";
    renderGraph();
  }

  // --- Hover Tooltip on Commit Graph Rows ---------------------------------
  function showCommitTip(row) {
    const c = state.commits[+row.getAttribute("data-i")];
    if (!c) return;
    gltip.textContent = "";
    gltip.appendChild(mk("div", "ts", c.subject));
    const pills = buildRefPills(c);
    if (pills.length) {
      const tr = mk("div", "tr");
      pills.forEach((p) => tr.appendChild(p));
      gltip.appendChild(tr);
    }
    const tm = mk("div", "tm");
    tm.innerHTML = `<span>${c.author}</span><span>·</span><span>${c.age} (${c.date})</span><span>·</span><code>${c.oid.slice(0, 9)}</code>`;
    gltip.appendChild(tm);
    gltip.hidden = false;

    const r = row.getBoundingClientRect();
    let y = r.bottom + 5;
    const th = gltip.offsetHeight;
    if (y + th > window.innerHeight - 10) y = r.top - th - 5;
    const x = Math.max(8, Math.min(r.left + 64, window.innerWidth - gltip.offsetWidth - 10));
    gltip.style.left = x + "px";
    gltip.style.top = y + "px";
  }

  function hideCommitTip() { gltip.hidden = true; }

  // --- Tabs, Gallery Pages, Presets, Theme, Drag Resize -------------------
  function setNarrowTab(tab) {
    state.narrowTab = tab;
    gitRegions.setAttribute("data-active-tab", tab);
    document.querySelectorAll(".gntab").forEach((b) => {
      const on = b.getAttribute("data-git-tab") === tab;
      b.classList.toggle("is-on", on);
      b.setAttribute("aria-selected", String(on));
    });
  }

  function setGalleryPage(page) {
    state.activePage = page;
    bgal.setAttribute("data-active-page", page);
    document.querySelectorAll(".gal-page").forEach((p) => {
      p.classList.toggle("is-active", p.getAttribute("data-page-panel") === page);
    });
    document.querySelectorAll(".gal-dot[data-page]").forEach((d) => {
      const on = d.getAttribute("data-page") === page && !d.classList.contains("empty");
      d.classList.toggle("active", on);
      d.setAttribute("aria-selected", String(on));
    });
  }

  function updateMetricBadge() {
    const w = Math.round(bgal.getBoundingClientRect().width);
    const mode = w >= 720 ? "Wide" : "Narrow";
    $("m-bgal-width").textContent = `#bgal · ${w}px · ${mode}`;
  }

  function applyPreset(preset) {
    state.preset = preset;
    root.setAttribute("data-preset", preset);
    document.querySelectorAll(".m-tab").forEach((t) => {
      const on = t.getAttribute("data-preset") === preset;
      t.classList.toggle("is-on", on);
      t.setAttribute("aria-selected", String(on));
    });

    // Reset busy remote buttons cleanly
    state.gitBusy = false;
    document.querySelectorAll(".gact-btn").forEach((b) => {
      b.disabled = false;
      b.classList.remove("is-busy");
      const act = b.getAttribute("data-git-act");
      b.querySelector("span").textContent = act.charAt(0).toUpperCase() + act.slice(1);
    });
    showFeedback("");

    if (preset === "wide") {
      root.style.setProperty("--bgal-w", "900px");
      state.bgalWidth = 900;
      setGalleryPage("git");
      resetWorktreeLight();
    } else if (preset === "narrow") {
      root.style.setProperty("--bgal-w", "380px");
      state.bgalWidth = 380;
      setGalleryPage("git");
      resetWorktreeLight();
    } else if (preset === "busy") {
      root.style.setProperty("--bgal-w", "900px");
      state.bgalWidth = 900;
      setGalleryPage("git");
      // Put Fetch into live busy state & Worktrees into mid-scan state
      const fetchBtn = document.querySelector('.gact-btn[data-git-act="fetch"]');
      document.querySelectorAll(".gact-btn").forEach((b) => { b.disabled = true; });
      if (fetchBtn) {
        fetchBtn.classList.add("is-busy");
        fetchBtn.querySelector("span").textContent = "Fetching…";
      }
      showFeedback("원격 저장소 Fetch 및 워크트리 WalkDir 스캔이 진행 중입니다…", "info");
      runWorktreeScan(true);
    }

    requestAnimationFrame(updateMetricBadge);
  }

  function toggleTheme() {
    state.theme = state.theme === "dark" ? "light" : "dark";
    root.setAttribute("data-theme", state.theme);
    const btn = $("m-theme-toggle");
    btn.setAttribute("aria-pressed", String(state.theme === "light"));
    $("m-theme-lbl").textContent = state.theme === "dark" ? "Dark" : "Light";
  }

  // --- Wire Events --------------------------------------------------------
  function bindEvents() {
    // Top mockup bar presets & theme
    document.querySelectorAll(".m-tab").forEach((tab) => {
      tab.addEventListener("click", () => applyPreset(tab.getAttribute("data-preset")));
    });
    $("m-theme-toggle").addEventListener("click", toggleTheme);

    // Optional left session rail toggle
    $("railbtn").addEventListener("click", () => {
      const collapsed = root.getAttribute("data-rail-collapsed") === "true";
      root.setAttribute("data-rail-collapsed", String(!collapsed));
      $("railbtn").setAttribute("aria-expanded", String(collapsed));
      requestAnimationFrame(updateMetricBadge);
    });

    // Narrow 3-Tab Switcher
    document.querySelectorAll(".gntab").forEach((btn) => {
      btn.addEventListener("click", () => setNarrowTab(btn.getAttribute("data-git-tab")));
    });

    // Review Ledger Pill & Mini Frontier Button -> Jump to 4c72b9d
    $("review-ledger-pill").addEventListener("click", jumpToReviewFrontier);
    $("frontier-mini-btn").addEventListener("click", jumpToReviewFrontier);

    // Remote Git Actions (Fetch / Pull / Push)
    document.querySelectorAll(".gact-btn").forEach((btn) => {
      btn.addEventListener("click", () => runGitAction(btn));
    });

    // Commit Graph interactions
    glrows.addEventListener("click", (e) => {
      if (e.target.closest(".glmore-b")) {
        loadMoreCommits();
        showToast("이전 커밋 4건을 추가로 불러왔습니다.");
        return;
      }
      const row = e.target.closest(".glr");
      if (row) {
        hideCommitTip();
        const c = state.commits[+row.getAttribute("data-i")];
        if (c) openCommitModal(c);
      }
    });
    glrows.addEventListener("keydown", (e) => {
      const row = e.target.closest(".glr");
      if (row && (e.key === "Enter" || e.key === " ")) {
        e.preventDefault();
        const c = state.commits[+row.getAttribute("data-i")];
        if (c) openCommitModal(c);
      }
    });
    glrows.addEventListener("mouseover", (e) => {
      const row = e.target.closest(".glr");
      if (row) showCommitTip(row);
      else hideCommitTip();
    });
    glrows.addEventListener("mouseleave", hideCommitTip);
    glscroll.addEventListener("scroll", hideCommitTip, { passive: true });

    // Working Tree Diff file click -> #gwin-back
    gfiles.addEventListener("click", (e) => {
      const row = e.target.closest(".gf[data-side]");
      if (row) openWorktreeDiffModal(row.getAttribute("data-side"), +row.getAttribute("data-i"));
    });

    // Worktrees Large Manual Refresh Button ((1729)=a)
    wtScanBtn.addEventListener("click", () => runWorktreeScan(false));

    // Worktrees Cleanup Request Button
    wtClean.addEventListener("click", () => {
      wtClean.disabled = true;
      wtNote.className = "wtnote is-ok";
      wtNote.textContent = "세션에 stale 워크트리(wt-legacy-drawer) 정리 요청을 전송했습니다.";
      showToast("Cleanup 요청이 현재 세션으로 전달되었습니다.");
      setTimeout(() => { wtClean.disabled = false; }, 1600);
    });

    // Modal close & copy hash
    $("gw-close").addEventListener("click", closeModal);
    gwinBack.addEventListener("click", (e) => {
      if (e.target === gwinBack) closeModal();
    });
    window.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && !gwinBack.hidden) {
        e.preventDefault();
        closeModal();
      }
    });
    $("gw-copy").addEventListener("click", () => {
      const btn = $("gw-copy");
      const span = btn.querySelector("span");
      const oid = btn.getAttribute("data-oid") || "";
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(oid).catch(() => {});
      }
      btn.classList.add("done");
      span.textContent = "복사됨!";
      setTimeout(() => {
        btn.classList.remove("done");
        span.textContent = "해시 복사";
      }, 1300);
    });

    // Gallery .gal-rail navigation & hover popover (left-anchored since rail is on right edge)
    const getPages = () => state.preset === "narrow"
      ? ["main", "p1", "workpanel", "ticketmap", "git"]
      : ["p1", "workpanel", "ticketmap", "git"];
    document.querySelectorAll(".gal-dot[data-page]").forEach((dot) => {
      dot.addEventListener("click", () => setGalleryPage(dot.getAttribute("data-page")));
      dot.addEventListener("mouseenter", () => {
        const lbl = dot.getAttribute("data-label");
        if (!lbl) return;
        galPop.textContent = lbl;
        galPop.hidden = false;
        const r = dot.getBoundingClientRect();
        galPop.style.left = Math.max(8, r.left - galPop.offsetWidth - 10) + "px";
        galPop.style.top = (r.top + r.height / 2 - galPop.offsetHeight / 2) + "px";
      });
      dot.addEventListener("mouseleave", () => { galPop.hidden = true; });
    });
    document.querySelectorAll("[data-return-git]").forEach((btn) => {
      btn.addEventListener("click", () => setGalleryPage("git"));
    });
    $("gal-prev").addEventListener("click", () => {
      const pages = getPages();
      const idx = pages.indexOf(state.activePage);
      setGalleryPage(pages[(idx - 1 + pages.length) % pages.length]);
    });
    $("gal-next").addEventListener("click", () => {
      const pages = getPages();
      const idx = pages.indexOf(state.activePage);
      setGalleryPage(pages[(idx + 1) % pages.length]);
    });

    // Draggable Left Edge Resize Handle (.bp-edge)
    const edge = $("bgal-edge");
    let dragStartX = 0;
    let dragStartW = 900;
    edge.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      edge.setPointerCapture(e.pointerId);
      edge.classList.add("drag");
      document.body.classList.add("is-resizing");
      dragStartX = e.clientX;
      dragStartW = bgal.getBoundingClientRect().width;
      root.setAttribute("data-preset", "custom");
    });
    edge.addEventListener("pointermove", (e) => {
      if (!edge.classList.contains("drag")) return;
      const delta = dragStartX - e.clientX;
      const maxW = Math.max(420, window.innerWidth - 280);
      const nextW = Math.round(Math.max(360, Math.min(maxW, dragStartW + delta)));
      state.bgalWidth = nextW;
      root.style.setProperty("--bgal-w", nextW + "px");
      updateMetricBadge();
    });
    const endDrag = () => {
      edge.classList.remove("drag");
      document.body.classList.remove("is-resizing");
    };
    edge.addEventListener("pointerup", endDrag);
    edge.addEventListener("pointercancel", endDrag);

    window.addEventListener("resize", updateMetricBadge);
  }

  // --- Boot ---------------------------------------------------------------
  renderGraph();
  renderWorkingTree();
  renderWorktrees();
  bindEvents();
  requestAnimationFrame(updateMetricBadge);
})();
