package wsdashboard

import (
	"path/filepath"
	"strings"
	"time"
)

// The session rail: the card every session page shows at its left (from a
// wide viewport always, else as an overlay the top-left arrow opens), listing
// the sessions this daemon serves grouped by workspace, then worktree. It is
// one shared render for every session page, keyed by language like the
// index: nothing in it depends on the viewed session, and the page marks its
// own row on the client. Like the session view it is built by a pure
// function from what the daemon read (buildRailView), so every rule is
// testable without a server; the reads live in railReader below, the
// worktree lists in railworktrees.go, and the last messages in
// railmessages.go.
//
// A workspace is a repository (its root worktree) or a plain non-git
// folder. Under a repository every worktree git lists is a row, a worktree
// with no session dimmed, except the ws pooled worktrees (under the
// repository's worktree pool roots, railPooled), which never show: their
// sessions, live or ended, are left out of the rail and its counts. Hiding
// is presentation only; such a session's own page still serves. A
// workspace the user stored (workspaces.go) shows whether
// or not it holds a session, and a stored repository lists its worktrees
// and their branches without one: any viewer who can add a workspace can
// list its worktrees anyway. Beyond the stored workspaces the rail shows
// nothing the index and the session pages do not already serve to the same
// viewers.

// railMessageRunes bounds a row's last message; the row shows one line.
const railMessageRunes = 160

// RailView is what the rail region renders.
type RailView struct {
	Workspaces         []RailWorkspace
	PendingWorkspaces  []RailWorkspace
	RejectedWorkspaces []RailWorkspace
	// Live counts the live sessions; CountPolicy sums their open policy
	// and check items, and CountApproval counts those waiting on a tool approval
	// (RailRow.Approval).
	Live, CountPolicy, CountApproval int
	// CountSleep counts the live hosted sessions that look idle (no
	// harness exit, no work, no approval wait): the sleep button is
	// disabled at zero. It is the rail's own reading; the runner decides
	// again when the list is asked and when the sleep runs.
	CountSleep int
	// CanAdd reports a daemon that keeps stored workspaces: the page then
	// shows the add-workspace and add-worktree buttons.
	CanAdd bool
	// CanSpawn reports a daemon that hosts lead sessions: the page then
	// shows the new-session button on every worktree row. A plain folder's
	// bar has none, since a lead session starts only at a Git worktree root
	// and the spawn route refuses any other. The rail is one render for
	// every viewer, so the page itself hides the button from a viewer the
	// spawn route would refuse.
	CanSpawn bool
	// LayoutEmpty reports a rail layout (rail_layout.go) that holds no
	// entry at all: a browser with a retired local order then imports it. LayoutGen is the layout's generation, which rises with
	// every change, so a browser that saw a change applied can tell a
	// render from before it.
	LayoutEmpty bool
	LayoutGen   uint64
}

// RailWorkspace is one repository or plain folder.
type RailWorkspace struct {
	// Name is Path's last element; Path the repository root or folder.
	Name, Path string
	MemoCount  int
	// Repo marks a repository: its sessions sit under Worktrees. A plain
	// folder lists them directly in Sessions and Ended.
	Repo bool
	// Stored marks a workspace in the stored list; the page offers to
	// forget it.
	Stored bool
	// Live counts the workspace's live sessions, and Approvals those of
	// them waiting on a tool approval.
	Live, Approvals int
	Worktrees       []RailWorktree
	// Sessions are the unfolded sessions, Ended the deleted ones, of a plain
	// folder; Archived its archived hosted leads (splitRailArchived).
	Sessions, Ended, Archived []RailRow
	newestRegisteredAt        time.Time
}

// ParentPath returns the parent directory prefix with a trailing path
// separator for display under the workspace name.
func (w RailWorkspace) ParentPath() string {
	if w.Path == "" {
		return ""
	}
	dir := filepath.Dir(filepath.Clean(w.Path))
	if dir == "" || dir == "." {
		return ""
	}
	sep := string(filepath.Separator)
	if strings.HasSuffix(dir, sep) {
		return dir
	}
	return dir + sep
}

// AllSessions flattens live (including nested children), ended, and archived
// session rows across plain-folder or repository worktrees.
func (w RailWorkspace) AllSessions() []RailRow {
	var out []RailRow
	var addLive func([]RailRow)
	addLive = func(rows []RailRow) {
		for _, r := range rows {
			out = append(out, r)
			if len(r.Children) > 0 {
				addLive(r.Children)
			}
		}
	}
	addGroup := func(live, ended, archived []RailRow) {
		addLive(live)
		out = append(out, ended...)
		out = append(out, archived...)
	}
	addGroup(w.Sessions, w.Ended, w.Archived)
	for _, wt := range w.Worktrees {
		addGroup(wt.Sessions, wt.Ended, wt.Archived)
	}
	return out
}

// LiveSessions returns the workspace's live rows, children included, in
// the workspace and in its worktrees: the rows a pending or rejected
// workspace renders for the notification scan.
func (w RailWorkspace) LiveSessions() []RailRow {
	var out []RailRow
	var add func([]RailRow)
	add = func(rows []RailRow) {
		for _, r := range rows {
			out = append(out, r)
			add(r.Children)
		}
	}
	add(w.Sessions)
	for _, wt := range w.Worktrees {
		add(wt.Sessions)
	}
	return out
}

// SessionCount returns the number of listed sessions in the workspace across
// live, ended, and archived folds.
func (w RailWorkspace) SessionCount() int {
	return len(w.AllSessions())
}

// RailWorktree is one worktree of a repository.
type RailWorktree struct {
	Path                                         string
	Origin, Admission                            string
	AdmissionRepository, AdmissionPath, Revision string
	// Root marks the repository's root worktree; Folder is Path's last
	// element (the page shows "root" for the root worktree instead).
	Root   bool
	Folder string
	// Branch is the checked-out branch, short form; Detached marks a
	// detached HEAD (Branch then empty).
	Branch   string
	Detached bool
	// Changes is the working-tree entry count (modified, staged, and
	// untracked) of the newest snapshot of a session in the worktree;
	// HasChanges is false when no such snapshot exists, and the page then
	// shows no count. The rail never reads a working tree itself.
	Changes    int
	HasChanges bool
	// Deletable marks a linked worktree the rail's delete offers: one the
	// repository's worktree list names, neither its primary worktree nor
	// under a ws worktree pool (worktreePoolRoots). The delete routes check
	// again; this only places the button.
	Deletable bool
	// Sessions are the unfolded sessions, Ended the deleted ones, Archived the
	// archived hosted leads (splitRailArchived).
	Sessions, Ended, Archived []RailRow
}

// Empty reports a worktree that holds no listed session; the page dims it.
func (w RailWorktree) Empty() bool {
	return len(w.Sessions) == 0 && len(w.Ended) == 0 && len(w.Archived) == 0
}

// LabelMax bounds a rename typed into a rail row, as SessionView.LabelMax.
func (RailView) LabelMax() int { return maxLabelRunes }
