package wsdashboard

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// Building the rail view: buildRailView groups listed sessions by workspace and
// worktree, then the partition, ordering, and archive passes shape the result.

// railInput is one listed session with what the rail shows of it.
type railInput struct {
	Session Session
	// Snapshot is the session's snapshot file, stale or not; nil when it has
	// none.
	Snapshot *wslive.Snapshot
	// Repo is the session's repository root (railRepoRoot), empty for a
	// plain folder. Linked reports a session root that is a linked worktree
	// (its .git is a file), which a missing worktree list cannot place
	// under the root worktree.
	Repo    string
	Linked  bool
	Message railMessage
	// CanRevive reports a daemon that hosts lead sessions: an ended row
	// then carries how it comes back (railRevive).
	CanRevive bool
	// AgentMeta identifies retained runner records after terminal handoff.
	AgentMeta AgentSessionMetaHost
	// Harnesses declares which harnesses' ended terminal sessions can be
	// taken over (DaemonOptions.Harnesses).
	Harnesses HarnessTraitSet
	// ExecJobs is the live exec job count under the session's project
	// (railExecJobs), as RailRow.ExecJobs; OwnExecJobs is the session's own
	// running hosted exec jobs (DaemonOptions.HostedExecJobs), as
	// RailRow.OwnExecJobs.
	ExecJobs    int
	OwnExecJobs int
}

// railExecJobs sums the live exec jobs (DaemonOptions.ExecJobs, by root)
// whose root is project or holds it.
func railExecJobs(jobs map[string]int, project string) int {
	if len(jobs) == 0 || project == "" {
		return 0
	}
	project = filepath.Clean(project)
	n := 0
	for root, c := range jobs {
		if railPathUnder(filepath.Clean(root), project) {
			n += c
		}
	}
	return n
}

// railMessage is a session's last conversation message as the rail shows it,
// plus the newest ring activity timestamp (LastAt, epoch ms) from the same
// bounded tail read.
type railMessage struct {
	Text   string
	User   bool
	LastAt int64
}

// buildRailView groups the listed sessions by workspace and worktree. Stopped
// and deleted sessions stay listed while the index holds them (while their
// registry directories and session records exist). repos holds the worktree
// lists read so far, by repository root; a
// repository without one shows the worktrees its sessions are rooted in.
//
// stored are the stored workspaces. A session in a repository always sits
// under its repository, which is one workspace with a stored one of the
// same root. A session in no repository joins the stored plain folder its
// root is at or under (the longest such folder), so a stored parent folder
// never takes a repository's sessions; without one its root is a workspace
// of its own. A stored workspace shows without sessions too.
func buildRailView(sessions []railInput, repos map[string]railRepo, stored []storedWorkspace, now time.Time) RailView {
	var view RailView
	storedAt := map[string]bool{}
	var folders []string
	// One workspace per identity (admissionPathKey, case-folded on Windows),
	// which the admission display and route also use: a stored spelling
	// wins, else the first one seen, so C:\Work\Repo stored and a session
	// at C:\work\repo make one workspace, not an empty stored one and a
	// pending twin the route refuses as stale.
	canon := map[string]string{}
	canonical := func(p string) string {
		k := admissionPathKey(p)
		if c, ok := canon[k]; ok {
			return c
		}
		canon[k] = p
		return p
	}
	for _, w := range stored {
		p := canonical(filepath.Clean(w.Path))
		storedAt[p] = true
		if !w.Repo {
			folders = append(folders, p)
		}
	}
	type member struct {
		in  railInput
		row RailRow
	}
	byWS := map[string][]member{}
	repoOf := map[string]bool{}
	// listOf is the repos key a workspace's sessions name (git's spelling of
	// the root): that list was read with their registrations, so it carries
	// the configured worktree pool a stored alias's list lacks.
	listOf := map[string]string{}
	for _, in := range sessions {
		reg := in.Session.Registration
		row := railRow(in, reg.Live(now), now)
		key := filepath.Clean(reg.Project)
		if in.Repo != "" {
			own := filepath.Clean(in.Repo)
			key = canonical(own)
			repoOf[key] = true
			if _, ok := repos[own]; ok && listOf[key] == "" {
				listOf[key] = own
			}
			if _, ok := byWS[key]; !ok {
				byWS[key] = nil
			}
			if railPooled(repos[own], own, in.Session.Project) {
				// Hidden, live or ended, and left out of every count; its
				// repository still shows as it would without it.
				continue
			}
		} else {
			best := ""
			for _, f := range folders {
				if railPathUnder(admissionPathKey(f), admissionPathKey(key)) && len(f) > len(best) {
					best = f
				}
			}
			if best != "" {
				key = best
			} else {
				key = canonical(key)
			}
		}
		byWS[key] = append(byWS[key], member{in: in, row: row})
		if row.Live {
			view.Live++
			view.CountPolicy += row.CountPolicy
			if row.Approval != nil {
				view.CountApproval++
			}
			if row.Hosted && !row.Stopped() && !row.Working && row.BackgroundTasks == 0 && row.Approval == nil {
				view.CountSleep++
			}
		}
	}
	for _, w := range stored {
		p := canonical(filepath.Clean(w.Path))
		if _, ok := byWS[p]; !ok {
			byWS[p] = nil
			if w.Repo {
				repoOf[p] = true
			}
		}
	}
	keys := make([]string, 0, len(byWS))
	for k := range byWS {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		members := byWS[key]
		ws := RailWorkspace{Name: lastElem(key), Path: key, Repo: repoOf[key], Stored: storedAt[key]}
		for _, m := range members {
			if m.row.Live {
				ws.Live++
			}
			if m.row.Approval != nil {
				ws.Approvals++
			}
			if at, err := time.Parse(time.RFC3339Nano, m.in.Session.Registration.RegisteredAt); err == nil {
				at = at.UTC()
				if ws.newestRegisteredAt.IsZero() || at.After(ws.newestRegisteredAt) {
					ws.newestRegisteredAt = at
				}
			}
		}
		if !ws.Repo {
			regs := map[string]Registration{}
			for _, m := range members {
				regs[m.row.ReplyID] = m.in.Session.Registration
				m.row.settingsWorktree = settingsID("worktree", key)
				if m.row.InSessionList() {
					ws.Sessions = append(ws.Sessions, m.row)
				} else {
					ws.Ended = append(ws.Ended, m.row)
				}
			}
			sortRailRows(ws.Sessions, regs)
			sortRailEnded(ws.Ended, regs)
			railMailbox(ws.Sessions, "", "")
			railMailbox(ws.Ended, "", "")
			view.Workspaces = append(view.Workspaces, ws)
			continue
		}
		// The list is picked by workspace identity: the one the sessions'
		// registrations were read with, else the exact spelling, else one
		// read under a spelling the stored one folds to.
		repo, ok := repos[listOf[key]]
		if !ok {
			repo, ok = repos[key]
		}
		if !ok {
			for p, r := range repos {
				if admissionPathKey(p) == admissionPathKey(key) {
					repo = r
					break
				}
			}
		}
		entries := repo.Entries
		if !repo.Read {
			entries = nil
		}
		// Each session sits under the listed worktree with the longest path
		// prefix of its root. A root no listed worktree holds (no list read
		// yet, or a worktree added since) sits under the root worktree when
		// it is a folder inside it, else is a worktree of its own. Paths
		// match by workspace identity (admissionPathKey), so on Windows a
		// spelling git and a session disagree on is still one worktree.
		type wtAcc struct {
			wt   RailWorktree
			snap *wslive.Snapshot
			regs map[string]Registration
		}
		accs := map[string]*wtAcc{}
		var order []string
		addWT := func(e railWorktreeEntry) *wtAcc {
			p := filepath.Clean(e.Path)
			k := admissionPathKey(p)
			if a, ok := accs[k]; ok {
				return a
			}
			a := &wtAcc{wt: RailWorktree{Path: p, Root: k == admissionPathKey(key), Folder: lastElem(p), Branch: e.Branch, Detached: e.Detached, AdmissionRepository: e.AdmissionRepository, AdmissionPath: e.AdmissionPath, Revision: e.Revision}, regs: map[string]Registration{}}
			accs[k] = a
			order = append(order, k)
			return a
		}
		for _, m := range members {
			project := filepath.Clean(m.in.Session.Project)
			best := ""
			for _, e := range entries {
				p := filepath.Clean(e.Path)
				if railPathUnder(admissionPathKey(p), admissionPathKey(project)) && len(p) > len(best) {
					best = p
				}
			}
			var a *wtAcc
			if best != "" {
				for _, e := range entries {
					if filepath.Clean(e.Path) == best {
						a = addWT(e)
						break
					}
				}
			} else if railPathUnder(admissionPathKey(key), admissionPathKey(project)) && !m.in.Linked {
				a = addWT(railWorktreeEntry{Path: key})
			} else {
				a = addWT(railWorktreeEntry{Path: project})
			}
			m.row.settingsWorktree = a.wt.SettingsID()
			if m.row.InSessionList() {
				a.wt.Sessions = append(a.wt.Sessions, m.row)
			} else {
				a.wt.Ended = append(a.wt.Ended, m.row)
			}
			a.regs[m.row.ReplyID] = m.in.Session.Registration
			if s := m.in.Snapshot; s != nil && s.Repo != nil && (a.snap == nil || s.UpdatedAt > a.snap.UpdatedAt) {
				a.snap = s
			}
		}
		// Every other listed worktree shows too, dimmed, except a pooled
		// one (its sessions are hidden, above) and one git marks prunable
		// (its folder is gone).
		for i, e := range entries {
			p := filepath.Clean(e.Path)
			if _, ok := accs[admissionPathKey(p)]; ok || e.Prunable {
				continue
			}
			if i > 0 && railPooled(repo, key, p) {
				continue
			}
			addWT(e)
		}
		// A linked worktree the list names is deletable unless it is
		// pooled.
		linked := map[string]bool{}
		var pools []string
		if len(entries) > 0 {
			pools = worktreePoolRoots(repo.Pools, filepath.Clean(entries[0].Path))
			for _, e := range entries[1:] {
				if !e.Prunable {
					linked[admissionPathKey(e.Path)] = true
				}
			}
		}
		for _, k := range order {
			a := accs[k]
			a.wt.Deletable = linked[k] && !a.wt.Root && !worktreePooled(pools, a.wt.Path)
			if a.snap != nil {
				r := a.snap.Repo
				a.wt.Changes, a.wt.HasChanges = r.Modified+r.Staged+r.Untracked, true
				if a.wt.Branch == "" && !a.wt.Detached {
					// A worktree no list names yet takes its branch from the
					// snapshot.
					a.wt.Branch, a.wt.Detached = r.Branch, r.Detached
				}
			}
			sortRailRows(a.wt.Sessions, a.regs)
			sortRailEnded(a.wt.Ended, a.regs)
			branch := a.wt.Branch
			if a.wt.Detached {
				branch = ""
			}
			railMailbox(a.wt.Sessions, a.wt.Path, branch)
			railMailbox(a.wt.Ended, a.wt.Path, branch)
			ws.Worktrees = append(ws.Worktrees, a.wt)
		}
		sort.SliceStable(ws.Worktrees, func(i, j int) bool {
			a, b := ws.Worktrees[i], ws.Worktrees[j]
			if a.Root != b.Root {
				return a.Root
			}
			return a.Path < b.Path
		})
		view.Workspaces = append(view.Workspaces, ws)
	}
	return view
}

// partitionRailWorkspaces splits presentation after layout, archive, admission,
// and memo passes: Workspaces retains only explicitly stored workspaces;
// unstored roots route to RejectedWorkspaces (plus session-less rejected paths
// for Undo) or PendingWorkspaces (omitting unstored roots with zero visible
// sessions).
func partitionRailWorkspaces(view *RailView, rejected []string) {
	if view == nil {
		return
	}
	rejSet := make(map[string]bool, len(rejected))
	for _, p := range rejected {
		if p != "" {
			rejSet[admissionPathKey(p)] = true
		}
	}
	var explicit, pending, rej []RailWorkspace
	storedSet := make(map[string]bool, len(view.Workspaces))
	seenRej := make(map[string]bool, len(rejected))
	for _, ws := range view.Workspaces {
		k := admissionPathKey(ws.Path)
		if ws.Stored {
			explicit = append(explicit, ws)
			storedSet[k] = true
			continue
		}
		if rejSet[k] {
			rej = append(rej, ws)
			seenRej[k] = true
			continue
		}
		if ws.SessionCount() > 0 {
			pending = append(pending, ws)
		}
	}
	for _, p := range rejected {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		k := admissionPathKey(p)
		if storedSet[k] || seenRej[k] {
			continue
		}
		seenRej[k] = true
		rej = append(rej, RailWorkspace{Name: lastElem(p), Path: p})
	}
	sort.SliceStable(pending, func(i, j int) bool {
		a, b := pending[i], pending[j]
		if !a.newestRegisteredAt.Equal(b.newestRegisteredAt) {
			return a.newestRegisteredAt.After(b.newestRegisteredAt)
		}
		return a.Path < b.Path
	})
	sort.SliceStable(rej, func(i, j int) bool {
		return rej[i].Path < rej[j].Path
	})
	view.Workspaces = explicit
	view.PendingWorkspaces = pending
	view.RejectedWorkspaces = rej
}

// sortRailRows orders unfolded sessions by registration, oldest first: the
// order does not move with heartbeats or activity, so a row stays where the
// user last saw it (the rail layout orders the rows after it,
// orderRailView).
func sortRailRows(rows []RailRow, regs map[string]Registration) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := regs[rows[i].ReplyID].RegisteredAt, regs[rows[j].ReplyID].RegisteredAt
		if a != b {
			return a < b
		}
		return rows[i].ReplyID < rows[j].ReplyID
	})
}

// sortRailEnded orders ended sessions by last seen, newest first.
func sortRailEnded(rows []RailRow, regs map[string]Registration) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := regs[rows[i].ReplyID].LastSeen(), regs[rows[j].ReplyID].LastSeen()
		if !a.Equal(b) {
			return a.After(b)
		}
		return rows[i].ReplyID < rows[j].ReplyID
	})
}

// sortRailArchived orders archived rows by last active time, newest first,
// with a stable ReplyID tie-breaker so relative timestamps read monotonically
// top-to-bottom without heartbeat pollution.
func sortRailArchived(rows []RailRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].LastActive != rows[j].LastActive {
			return rows[i].LastActive > rows[j].LastActive
		}
		return rows[i].ReplyID < rows[j].ReplyID
	})
}

// splitRailArchived moves each archived row out of its group's ended list
// into the group's Archived list, sorted by LastActive descending.
// It runs after orderRailView, which treats an archived row as ended.
func splitRailArchived(view *RailView) {
	split := func(ended, archived *[]RailRow) {
		var keep []RailRow
		for _, r := range *ended {
			if r.Archived {
				*archived = append(*archived, r)
			} else {
				keep = append(keep, r)
			}
		}
		sortRailArchived(*archived)
		*ended = keep
	}
	for i := range view.Workspaces {
		w := &view.Workspaces[i]
		split(&w.Ended, &w.Archived)
		for j := range w.Worktrees {
			split(&w.Worktrees[j].Ended, &w.Worktrees[j].Archived)
		}
	}
}
