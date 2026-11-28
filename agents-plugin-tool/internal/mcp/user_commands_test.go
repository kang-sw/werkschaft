package mcp

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// userCommandTestHost is the daemon's user commands over the real registry: the
// conversations in leads are open hosted leads, bound to one worktree.
type userCommandTestHost struct {
	reg   *wsusercmd.Registry
	root  string
	leads map[string]bool
}

func (h *userCommandTestHost) Lead(conversation string) error {
	if !h.leads[conversation] {
		return errors.New("not an open hosted lead")
	}
	return nil
}

func (h *userCommandTestHost) Register(conversation string, in wsusercmd.Input) (wsusercmd.Command, wsusercmd.Outcome, error) {
	return h.reg.Set(conversation, h.root, in)
}

func (h *userCommandTestHost) Unregister(conversation, id string) error {
	return h.reg.Remove(conversation, h.root, id)
}

type userCommandFixture struct {
	t    *testing.T
	s    *Server
	key  string
	root string
	host *userCommandTestHost
}

func newUserCommandFixture(t *testing.T) *userCommandFixture {
	t.Helper()
	useLeadProfile(t)
	root := t.TempDir()
	mustWrite(t, root, "README.md", "x\n")
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	reg, err := wsusercmd.Open(filepath.Join(t.TempDir(), wsusercmd.StoreName), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	host := &userCommandTestHost{reg: reg, root: "/worktree", leads: map[string]bool{"lead": true}}
	s := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, s, 900003, root, nil))
	s.conversation = "lead"
	s.userCommands = func() UserCommandHost { return host }
	return &userCommandFixture{t: t, s: s, key: key, root: root, host: host}
}

func (f *userCommandFixture) call(name string, args map[string]any) (string, bool) {
	f.t.Helper()
	return callExecTool(f.t, f.s, f.key, name, args)
}

func (f *userCommandFixture) listed(name string) bool {
	raw, _ := json.Marshal(f.s.filteredTools())
	return strings.Contains(string(raw), `"name":"`+name+`"`)
}

func TestUserCommandToolsRegisterReplaceUnregister(t *testing.T) {
	f := newUserCommandFixture(t)
	args := map[string]any{"id": "build", "label": "Build", "description": "compile", "command": "make all"}
	text, isErr := f.call("session.register_user_command", args)
	if isErr || !strings.Contains(text, "id: build") || !strings.Contains(text, "status: registered") {
		t.Fatalf("register = %q, %v", text, isErr)
	}
	revision := regexp.MustCompile(`revision: (\d+)`).FindStringSubmatch(text)
	if revision == nil {
		t.Fatalf("no revision in %q", text)
	}
	// The same definition again: unchanged, same revision.
	again, isErr := f.call("session.register_user_command", args)
	if isErr || !strings.Contains(again, "status: unchanged") || !strings.Contains(again, "revision: "+revision[1]) {
		t.Fatalf("again = %q, %v", again, isErr)
	}
	args["command"] = "make clean all"
	replaced, isErr := f.call("session.register_user_command", args)
	if isErr || !strings.Contains(replaced, "status: replaced") || strings.Contains(replaced, "revision: "+revision[1]) {
		t.Fatalf("replace = %q, %v", replaced, isErr)
	}
	if got, ok := f.host.reg.Get("lead", "/worktree", "build"); !ok || got.Line != "make clean all" {
		t.Fatalf("stored %+v %v", got, ok)
	}
	// Optional prompt and cwd round-trip cleanly.
	withExtras, isErr := f.call("session.register_user_command", map[string]any{
		"id":      "lint",
		"label":   "Lint",
		"command": "make lint",
		"prompt":  "Fix any lint errors in pkg/",
		"cwd":     "pkg",
	})
	if isErr || !strings.Contains(withExtras, "status: registered") {
		t.Fatalf("with prompt+cwd = %q, %v", withExtras, isErr)
	}
	if got, ok := f.host.reg.Get("lead", "/worktree", "lint"); !ok || got.Prompt != "Fix any lint errors in pkg/" || got.Cwd != "pkg" || got.Source != wsusercmd.SourceAgent {
		t.Fatalf("stored with prompt+cwd: %+v %v", got, ok)
	}
	// Invalid cwd syntax is refused cleanly.
	if text, isErr := f.call("session.register_user_command", map[string]any{
		"id":      "badcwd",
		"label":   "Bad",
		"command": "true",
		"cwd":     "a\nb",
	}); !isErr || !strings.Contains(text, "cwd") {
		t.Fatalf("bad cwd = %q, %v", text, isErr)
	}
	jsonText, isErr := f.call("session.register_user_command", map[string]any{"id": "b2", "label": "B2", "command": "true", "format": "json"})
	var parsed struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		Status   string `json:"status"`
	}
	if isErr || json.Unmarshal([]byte(jsonText), &parsed) != nil || parsed.ID != "b2" || parsed.Revision == 0 || parsed.Status != "registered" {
		t.Fatalf("json = %q, %v", jsonText, isErr)
	}
	if text, isErr := f.call("session.unregister_user_command", map[string]any{"id": "build"}); isErr || !strings.Contains(text, "status: unregistered") {
		t.Fatalf("unregister = %q, %v", text, isErr)
	}
	if text, isErr := f.call("session.unregister_user_command", map[string]any{"id": "build"}); !isErr || !strings.Contains(text, "no such user command") {
		t.Fatalf("second unregister = %q, %v", text, isErr)
	}
}

func TestUserCommandToolsRefuseOverLimitsClearly(t *testing.T) {
	f := newUserCommandFixture(t)
	for i := range wsusercmd.MaxPerScope {
		id := "c" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if text, isErr := f.call("session.register_user_command", map[string]any{"id": id, "label": id, "command": "true"}); isErr {
			t.Fatalf("command %d: %s", i, text)
		}
	}
	text, isErr := f.call("session.register_user_command", map[string]any{"id": "one-more", "label": "x", "command": "true"})
	if !isErr || !strings.Contains(text, "at most 32 user commands") {
		t.Fatalf("33rd = %q, %v", text, isErr)
	}
	// Replacing at the cap works.
	if text, isErr := f.call("session.register_user_command", map[string]any{"id": "caa", "label": "x", "command": "echo replaced"}); isErr || !strings.Contains(text, "status: replaced") {
		t.Fatalf("replace at the cap = %q, %v", text, isErr)
	}
	for name, args := range map[string]map[string]any{
		"label":       {"id": "a", "label": strings.Repeat("l", 65), "command": "true"},
		"description": {"id": "a", "label": "l", "description": strings.Repeat("d", 257), "command": "true"},
		"command":     {"id": "a", "label": "l", "command": strings.Repeat("c", 4097)},
	} {
		if text, isErr := f.call("session.register_user_command", args); !isErr || !strings.Contains(text, name) || !strings.Contains(text, "at most") {
			t.Fatalf("%s = %q, %v", name, text, isErr)
		}
	}
}

// TestUserCommandToolsAreListedOnlyToAHostedLead: advertised to an authenticated
// open hosted lead using its own binding; refused (and unlisted) for a
// tokenless transport, a non-lead conversation and a daemon without the store;
// a supplied key or reply ID never selects the owner.
func TestUserCommandToolsAreListedOnlyToAHostedLead(t *testing.T) {
	f := newUserCommandFixture(t)
	for _, name := range []string{"session.register_user_command", "session.unregister_user_command"} {
		if !f.listed(name) {
			t.Fatalf("%s is not listed to a hosted lead", name)
		}
		if !slices.Contains(LeadToolNames(), name) {
			t.Fatalf("the runtime inventory lacks %s", name)
		}
	}
	args := map[string]any{"id": "build", "label": "Build", "command": "make"}
	// A non-lead conversation: unlisted and refused.
	f.s.conversation = "worker"
	if f.listed("session.register_user_command") {
		t.Fatal("listed to a non-lead conversation")
	}
	if text, isErr := f.call("session.register_user_command", args); !isErr || !strings.Contains(text, "open hosted lead") {
		t.Fatalf("non-lead = %q, %v", text, isErr)
	}
	// A tokenless transport (no trusted binding): a lead key alone never names the owner.
	f.s.conversation = ""
	if f.listed("session.unregister_user_command") {
		t.Fatal("listed to a connection with no binding")
	}
	if text, isErr := f.call("session.register_user_command", args); !isErr || !strings.Contains(text, "session token") {
		t.Fatalf("tokenless = %q, %v", text, isErr)
	}
	// An arbitrary caller-supplied reply ID is an undeclared argument.
	f.s.conversation = "lead"
	forged := map[string]any{"id": "build", "label": "Build", "command": "make", "reply_id": "lead", "conversation": "other"}
	if text, isErr := f.call("session.register_user_command", forged); !isErr {
		t.Fatalf("forged owner argument admitted: %q", text)
	}
	if len(f.host.reg.List("lead", "/worktree")) != 0 {
		t.Fatal("a refused call stored a command")
	}
	// A daemon whose store could not open.
	f.s.userCommands = nil
	if f.listed("session.register_user_command") {
		t.Fatal("listed with no store")
	}
	if text, isErr := f.call("session.register_user_command", args); !isErr || !strings.Contains(text, "unavailable") {
		t.Fatalf("no store = %q, %v", text, isErr)
	}
	// An allowlisted profile (a clerk) stays gated even for a lead.
	f.s.userCommands = func() UserCommandHost { return f.host }
	t.Setenv(envAllowedTools, "git.status")
	if text, isErr := f.call("session.register_user_command", args); !isErr || !strings.Contains(text, "tool not available") {
		t.Fatalf("allowlist = %q, %v", text, isErr)
	}
}

// TestUserCommandToolsAreLeadOnlyForKeys: a delegate or leaf key may not call
// them even on a hosted connection.
func TestUserCommandToolsAreLeadOnlyForKeys(t *testing.T) {
	f := newUserCommandFixture(t)
	for _, role := range []toolRole{roleDelegate, roleLeaf} {
		key, err := f.s.sessions.mint(f.root, role, f.key)
		if err != nil {
			t.Fatal(err)
		}
		text, isErr := callExecTool(t, f.s, key, "session.register_user_command", map[string]any{"id": "x", "label": "x", "command": "true"})
		if !isErr {
			t.Fatalf("%s key admitted: %q", role, text)
		}
	}
	if len(f.host.reg.List("lead", "/worktree")) != 0 {
		t.Fatal("a refused call stored a command")
	}
}

func TestUserCommandToolDescriptionsAreShippedText(t *testing.T) {
	for _, tool := range userCommandTools() {
		description, _ := tool["description"].(string)
		for _, leak := range []string{"ws/", "wsflow", "ws:", "agents-plugin", "ai-docs", "ticket", "devenv", "scripts/install-claude-plugin.sh"} {
			if strings.Contains(description, leak) {
				t.Errorf("%v description contains %q", tool["name"], leak)
			}
		}
		if regexp.MustCompile(`\b26\d{4}-|\b[0-9a-f]{7,40}\b`).MatchString(description) {
			t.Errorf("%v description cites a ticket stem or commit", tool["name"])
		}
	}
}
