package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

const (
	planUsageHistoryCache      = "plan-usage-history.json"
	// Retained name check: the persisted five-hour history file name
	// (TestPlanUsageCodexFiveHourT0CompatibilityAndSeparateFile).
	planUsageCodexHistoryCache = "plan-usage-codex-history.json"
	planUsageHistoryAge        = 14 * 24 * time.Hour
	planUsageHistoryCap        = 5000
)

// Only these fields cross the viewer boundary. Extensions in the private file
// survive a supported-version round trip, but are never projected to viewers.
type planUsageSample struct {
	Harness  string  `json:"harness"`
	Group    string  `json:"group,omitempty"`
	Kind     string  `json:"kind"`
	Source   string  `json:"source"`
	SlotID   string  `json:"slotId,omitempty"`
	ResetsAt int64   `json:"resetsAt"`
	AsOf     int64   `json:"asOf"`
	Percent  float64 `json:"percent"`
}
type planUsageHistoryProjection struct {
	SchemaVersion int               `json:"schema_version"`
	Status        string            `json:"status"`
	Samples       []planUsageSample `json:"samples"`
}
type planUsageHistoryRecord struct {
	Sample planUsageSample
	Fields map[string]json.RawMessage
	seq    int64
	hasSeq bool
}
type planUsageHistoryFile struct {
	Fields  map[string]json.RawMessage
	Records []planUsageHistoryRecord
}
type planUsageHistorySpec struct {
	cacheName    string
	storeKey     string
	valid        func(planUsageSample) bool
	requiredKeys []string
}

var defaultHistorySpec = planUsageHistorySpec{
	cacheName:    planUsageHistoryCache,
	storeKey:     wsmeta.UsageHistoryDefault,
	valid:        validPlanUsageSample,
	requiredKeys: []string{"harness", "kind", "source", "resetsAt", "asOf", "percent"},
}

var codexFiveHourHistorySpec = planUsageHistorySpec{
	cacheName:    planUsageCodexHistoryCache,
	storeKey:     wsmeta.UsageHistoryCodex,
	valid:        validCodexFiveHourSample,
	requiredKeys: []string{"harness", "kind", "source", "resetsAt", "asOf", "percent"},
}

// validCodexFiveHourSample and validPlanUsageSample read persisted history
// bytes (plan-usage-codex-history.json, plan-usage-history.json), whose
// "harness" and "source" values were written under these names. They name the
// harnesses in the file's own format, which registration data cannot reread
// ((1881) survivor; TestPlanUsageHistoryValidatorsKeepPersistedNames).
func validCodexFiveHourSample(v planUsageSample) bool {
	if v.Group != "" {
		return false
	}
	sourceOK := v.Harness == "codex" && (v.Source == "via Codex CLI" || v.Source == "via Pi")
	return sourceOK && v.Kind == wslive.LimitFiveHour && v.AsOf > 0 && v.ResetsAt >= 0 && validUsage(v.Percent)
}

func validPlanUsageSample(v planUsageSample) bool {
	// Retained name check: the harness and source labels of the legacy history file
	// are persisted bytes earlier writers wrote (TestPlanUsageHistoryValidatorsKeepPersistedNames).
	if v.Group != "" {
		return false
	}
	sourceOK := v.Harness == "claude" && v.Source == "Claude push" || v.Harness == "codex" && (v.Source == "via Codex CLI" || v.Source == "via Pi")
	kindOK := v.Kind == "seven_day" || v.Harness == "claude" && v.Kind == "five_hour"
	return sourceOK && kindOK && v.AsOf > 0 && v.ResetsAt >= 0 && validUsage(v.Percent)
}

// planUsageHistoryMemo holds the parsed file and the viewer projection built
// from it. Guarded by planUsageStore.mu.
type planUsageHistoryMemo struct {
	ok   bool
	stat planUsageHistoryStat
	file planUsageHistoryFile
	err  error // content verdict (corrupt/newer); IO errors are never memoized
	rev  uint64

	viewRev   uint64
	viewKey   string
	viewUntil int64
	view      json.RawMessage
	// viewHas reports whether view's history held any unexpired sample.
	viewHas bool
}

// planUsageHistoryStat identifies the file version the memo reflects.
type planUsageHistoryStat struct {
	missing bool
	size    int64
	modTime int64
}

func (s *planUsageStore) statHistory(path string) (planUsageHistoryStat, error) {
	stat := os.Stat
	if s.statFile != nil {
		stat = s.statFile
	}
	info, err := stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return planUsageHistoryStat{missing: true}, nil
	}
	if err != nil {
		return planUsageHistoryStat{}, err
	}
	return planUsageHistoryStat{size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

// The parsed file is memoized by its stat (size, mtime): an unchanged file
// costs one stat and no read. Any other writer's replace changes the stat and
// forces a re-read before the next write, so a newer-version or explicitly
// repaired file is still never overwritten from a stale snapshot. Stat comes
// before read: a replace in between leaves an older stat on newer content,
// which costs only one extra read. This assumes one writer per directory (the
// daemon): a foreign replace landing between our own write and its stat, or
// one keeping both size and mtime, goes unnoticed. The result is shared with
// the memo and must not be mutated. Caller holds s.mu.
func (s *planUsageStore) readHistoryLocked() (planUsageHistoryFile, error) {
	return s.readHistoryFileLocked(&s.hist, defaultHistorySpec)
}

func (s *planUsageStore) loadHistoryFromMetaLocked(memo *planUsageHistoryMemo, spec planUsageHistorySpec, snap *wsmeta.Snapshot) {
	empty := planUsageHistoryFile{Fields: map[string]json.RawMessage{}}
	if snap != nil {
		if issue, ok := snap.ImportIssues[spec.cacheName]; ok {
			memo.ok, memo.file, memo.err = true, empty, fmt.Errorf("usage history %s: %s", issue.Status, issue.Detail)
			memo.rev++
			return
		}
		if uh, ok := snap.UsageHistory[spec.storeKey]; ok {
			h := planUsageHistoryFile{Fields: map[string]json.RawMessage{}}
			if len(uh.Extra) > 0 {
				_ = json.Unmarshal(uh.Extra, &h.Fields)
				if h.Fields == nil {
					h.Fields = map[string]json.RawMessage{}
				}
			}
			for _, sample := range uh.Samples {
				var v planUsageSample
				var fields map[string]json.RawMessage
				if json.Unmarshal(sample.Body, &v) != nil || !spec.valid(v) || json.Unmarshal(sample.Body, &fields) != nil {
					memo.ok, memo.file, memo.err = true, empty, errors.New("invalid usage history sample")
					memo.rev++
					return
				}
				for _, key := range spec.requiredKeys {
					if value, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
						memo.ok, memo.file, memo.err = true, empty, errors.New("incomplete usage history sample")
						memo.rev++
						return
					}
				}
				h.Records = append(h.Records, planUsageHistoryRecord{
					Sample: v,
					Fields: fields,
					seq:    sample.Seq,
					hasSeq: true,
				})
			}
			memo.ok, memo.file, memo.err = true, h, nil
			memo.rev++
			return
		}
	}
	memo.ok, memo.file, memo.err = true, empty, nil
	memo.rev++
}

func (s *planUsageStore) readHistoryFileLocked(memo *planUsageHistoryMemo, spec planUsageHistorySpec) (planUsageHistoryFile, error) {
	empty := planUsageHistoryFile{Fields: map[string]json.RawMessage{}}
	if s.meta != nil {
		if !memo.ok {
			memo.ok, memo.file = true, empty
		}
		return memo.file, memo.err
	}
	path := filepath.Join(s.dir, spec.cacheName)
	stat, err := s.statHistory(path)
	if err != nil {
		memo.ok = false
		return empty, err
	}
	if memo.ok && memo.stat == stat {
		return memo.file, memo.err
	}
	h, err := empty, error(nil)
	if !stat.missing {
		read := os.ReadFile
		if s.readFile != nil {
			read = s.readFile
		}
		raw, readErr := read(path)
		switch {
		case errors.Is(readErr, os.ErrNotExist):
		case readErr != nil:
			// A transient IO failure (a scanner holding the file) must not pin
			// "unavailable" until the file next changes.
			memo.ok = false
			return empty, readErr
		default:
			h, err = parsePlanUsageHistoryWith(raw, spec)
		}
	}
	memo.ok, memo.stat, memo.file, memo.err = true, stat, h, err
	memo.rev++
	return h, err
}

func parsePlanUsageHistory(raw []byte) (planUsageHistoryFile, error) {
	return parsePlanUsageHistoryWith(raw, defaultHistorySpec)
}

func parsePlanUsageHistoryWith(raw []byte, spec planUsageHistorySpec) (planUsageHistoryFile, error) {
	h := planUsageHistoryFile{Fields: map[string]json.RawMessage{}}
	if err := json.Unmarshal(raw, &h.Fields); err != nil {
		return h, err
	}
	var version int
	if json.Unmarshal(h.Fields["schema_version"], &version) != nil || version != 1 {
		return h, errors.New("unsupported usage history")
	}
	var records []json.RawMessage
	if json.Unmarshal(h.Fields["samples"], &records) != nil || records == nil {
		return h, errors.New("invalid usage history")
	}
	for _, raw := range records {
		var v planUsageSample
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &v) != nil || !spec.valid(v) || json.Unmarshal(raw, &fields) != nil {
			return h, errors.New("invalid usage history sample")
		}
		// All required members must be present, including a truthful zero/unknown reset.
		for _, key := range spec.requiredKeys {
			if value, ok := fields[key]; !ok || string(value) == "null" {
				return h, errors.New("incomplete usage history sample")
			}
		}
		h.Records = append(h.Records, planUsageHistoryRecord{Sample: v, Fields: fields})
	}
	return h, nil
}

// withSampleFields returns a fresh copy of fields with the six known members
// set from v; unknown extension members carry over unchanged.
func withSampleFields(fields map[string]json.RawMessage, v planUsageSample) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(fields)+6)
	for k, raw := range fields {
		out[k] = raw
	}
	raw, _ := json.Marshal(v)
	var known map[string]json.RawMessage
	_ = json.Unmarshal(raw, &known)
	for k, raw := range known {
		out[k] = raw
	}
	return out
}

func prunePlanUsageHistory(records []planUsageHistoryRecord, now int64) []planUsageHistoryRecord {
	kept := make([]planUsageHistoryRecord, 0, len(records))
	cutoff := now - planUsageHistoryAge.Milliseconds()
	for _, record := range records {
		if record.Sample.AsOf >= cutoff {
			kept = append(kept, record)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i].Sample, kept[j].Sample
		if a.AsOf != b.AsOf {
			return a.AsOf < b.AsOf
		}
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.SlotID != b.SlotID {
			return a.SlotID < b.SlotID
		}
		return strconv.FormatInt(a.ResetsAt, 10) < strconv.FormatInt(b.ResetsAt, 10)
	})
	counts := map[[3]string]int{}
	newest := make([]planUsageHistoryRecord, 0, len(kept))
	for i := len(kept) - 1; i >= 0; i-- {
		key := [3]string{kept[i].Sample.Harness, kept[i].Sample.Group, kept[i].Sample.Kind}
		if counts[key] < planUsageHistoryCap {
			newest = append(newest, kept[i])
			counts[key]++
		}
	}
	for i, j := 0, len(newest)-1; i < j; i, j = i+1, j-1 {
		newest[i], newest[j] = newest[j], newest[i]
	}
	return newest
}

// A record's Fields always hold its complete JSON object: the parsed members
// of a read record, withSampleFields for a new or replaced one. The memo is
// never mutated in place; a successful write becomes the new memo.
func (s *planUsageStore) appendHistoryLocked(samples []planUsageSample, now int64) {
	s.appendHistoryFileLocked(&s.hist, defaultHistorySpec, samples, now)
}

func (s *planUsageStore) appendHistoryFileLocked(memo *planUsageHistoryMemo, spec planUsageHistorySpec, samples []planUsageSample, now int64) {
	if len(samples) == 0 {
		return
	}
	current, err := s.readHistoryFileLocked(memo, spec)
	if err != nil {
		return
	} // Preserve corrupt/newer files for explicit repair.
	type identity struct {
		harness, group, kind, source, slotID string
		reset, at                            int64
	}
	key := func(v planUsageSample) identity {
		return identity{v.Harness, v.Group, v.Kind, v.Source, v.SlotID, v.ResetsAt, v.AsOf}
	}
	h := planUsageHistoryFile{Fields: make(map[string]json.RawMessage, len(current.Fields)+2), Records: append([]planUsageHistoryRecord(nil), current.Records...)}
	for k, raw := range current.Fields {
		h.Fields[k] = raw
	}
	indices := map[identity]int{}
	for i, r := range h.Records {
		indices[key(r.Sample)] = i
	}
	changed := false
	for _, v := range samples {
		if !spec.valid(v) {
			continue
		}
		if i, ok := indices[key(v)]; ok {
			// Re-pushing an observation already stored as is writes nothing.
			if h.Records[i].Sample == v {
				continue
			}
			h.Records[i] = planUsageHistoryRecord{
				Sample: v,
				Fields: withSampleFields(h.Records[i].Fields, v),
				seq:    h.Records[i].seq,
				hasSeq: h.Records[i].hasSeq,
			}
		} else {
			indices[key(v)] = len(h.Records)
			h.Records = append(h.Records, planUsageHistoryRecord{Sample: v, Fields: withSampleFields(nil, v)})
		}
		changed = true
	}
	if !changed {
		return
	}
	h.Records = prunePlanUsageHistory(h.Records, now)
	// An accepted sample the prune drops at once (past the age or the cap)
	// leaves the content as read: compare in memory and skip the write.
	if sameHistoryRecords(h.Records, current.Records) {
		return
	}
	if s.meta != nil {
		oldBySeq := make(map[int64]planUsageSample, len(current.Records))
		for _, r := range current.Records {
			if r.hasSeq {
				oldBySeq[r.seq] = r.Sample
			}
		}
		keptSeq := make(map[int64]bool, len(h.Records))
		var adds, updates []wsmeta.UsageSample
		var addIndices []int
		for i := range h.Records {
			r := &h.Records[i]
			if !r.hasSeq {
				body, err := json.Marshal(r.Fields)
				if err != nil {
					return
				}
				adds = append(adds, wsmeta.UsageSample{
					Harness:  r.Sample.Harness,
					Group:    r.Sample.Group,
					Kind:     r.Sample.Kind,
					Source:   r.Sample.Source,
					ResetsAt: r.Sample.ResetsAt,
					AsOf:     r.Sample.AsOf,
					Percent:  r.Sample.Percent,
					Body:     body,
				})
				addIndices = append(addIndices, i)
				continue
			}
			keptSeq[r.seq] = true
			if old, ok := oldBySeq[r.seq]; !ok || old != r.Sample {
				body, err := json.Marshal(r.Fields)
				if err != nil {
					return
				}
				updates = append(updates, wsmeta.UsageSample{
					Seq:      r.seq,
					Harness:  r.Sample.Harness,
					Group:    r.Sample.Group,
					Kind:     r.Sample.Kind,
					Source:   r.Sample.Source,
					ResetsAt: r.Sample.ResetsAt,
					AsOf:     r.Sample.AsOf,
					Percent:  r.Sample.Percent,
					Body:     body,
				})
			}
		}
		var deleteSeqs []int64
		for _, r := range current.Records {
			if r.hasSeq && !keptSeq[r.seq] {
				deleteSeqs = append(deleteSeqs, r.seq)
			}
		}
		addedSeqs, err := s.meta.MutateUsageHistory(context.Background(), spec.storeKey, adds, updates, deleteSeqs)
		if err != nil {
			return
		}
		for k, idx := range addIndices {
			if k < len(addedSeqs) {
				h.Records[idx].seq = addedSeqs[k]
				h.Records[idx].hasSeq = true
			}
		}
		memo.ok, memo.file, memo.err = true, h, nil
		memo.rev++
		return
	}
	records := make([]map[string]json.RawMessage, 0, len(h.Records))
	for _, r := range h.Records {
		records = append(records, r.Fields)
	}
	h.Fields["schema_version"] = json.RawMessage(`1`)
	h.Fields["samples"], err = json.Marshal(records)
	if err != nil {
		return
	}
	raw, err := json.Marshal(h.Fields)
	if err != nil || s.write(spec.cacheName, raw) != nil {
		return
	}
	// Adopt what was just written so the next read costs one stat; without a
	// stat, drop the memo and let the next read reload.
	stat, err := s.statHistory(filepath.Join(s.dir, spec.cacheName))
	if err != nil || stat.missing {
		memo.ok = false
		return
	}
	memo.ok, memo.stat, memo.file, memo.err = true, stat, h, nil
	memo.rev++
}

// sameHistoryRecords reports whether two record lists hold the same samples in
// the same order. A record's Fields change only alongside its Sample (see
// appendHistoryLocked), so equal samples mean equal written content.
func sameHistoryRecords(a, b []planUsageHistoryRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Sample != b[i].Sample {
			return false
		}
	}
	return true
}

// history is the full pruned history; viewers get historyView.
func (s *planUsageStore) history(now int64) planUsageHistoryProjection {
	if s == nil {
		return planUsageHistoryProjection{SchemaVersion: 1, Status: "empty", Samples: []planUsageSample{}}
	}
	return s.historyFile(&s.hist, defaultHistorySpec, now)
}

func (s *planUsageStore) historyFile(memo *planUsageHistoryMemo, spec planUsageHistorySpec, now int64) planUsageHistoryProjection {
	p := planUsageHistoryProjection{SchemaVersion: 1, Status: "empty", Samples: []planUsageSample{}}
	if s == nil {
		return p
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h, err := s.readHistoryFileLocked(memo, spec)
	if err != nil {
		p.Status = "unavailable"
		return p
	}
	for _, r := range prunePlanUsageHistory(h.Records, now) {
		p.Samples = append(p.Samples, r.Sample)
	}
	if len(p.Samples) > 0 {
		p.Status = "ok"
	}
	return p
}

// historyView is the ?history=1 member: the status of the full history, but
// only the samples trend() in assets/usage.js can use for the entry windows
// sent in the same response. The encoded JSON is memoized by history
// revision and entries until the age prune would next drop a sample.
func (s *planUsageStore) historyView(entries []planUsageEntry, now int64) json.RawMessage {
	if s == nil {
		raw, _ := json.Marshal(planUsageHistoryProjection{SchemaVersion: 1, Status: "empty", Samples: []planUsageSample{}})
		return raw
	}
	return s.historyFileView(&s.hist, defaultHistorySpec, entriesWithoutAgy(entries), now)
}

func (s *planUsageStore) codexFiveHourHistory(now int64) planUsageHistoryProjection {
	if s == nil {
		return planUsageHistoryProjection{SchemaVersion: 1, Status: "empty", Samples: []planUsageSample{}}
	}
	return s.historyFile(&s.codexHist, codexFiveHourHistorySpec, now)
}

// Retained name check: unified codex_openai entries project against the
// persisted "codex" history series
// (TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe).
func historyHarnessFor(harness string) string {
	if harness == codexOpenAIUnifiedHarness {
		return "codex"
	}
	return harness
}

// codexFiveHourHistoryView returns the projected ?history=1 member for Codex
// five-hour windows, and reports whether any non-expired Codex five-hour
// samples exist in the separate history file.
func (s *planUsageStore) codexFiveHourHistoryView(entries []planUsageEntry, now int64) (json.RawMessage, bool) {
	if s == nil {
		return nil, false
	}
	var filtered []planUsageEntry
	for _, e := range entries {
		histHarness := historyHarnessFor(e.Harness)
		if !s.harnesses.separateFiveHour(histHarness) {
			continue
		}
		var windows []planUsageWindow
		for _, w := range e.Windows {
			if w.Kind == wslive.LimitFiveHour {
				windows = append(windows, w)
			}
		}
		if len(windows) > 0 {
			copyEntry := e
			copyEntry.Harness = histHarness
			copyEntry.Windows = windows
			filtered = append(filtered, copyEntry)
		}
	}
	// Only a Codex five-hour entry reads this member (usage.js), so a poll
	// without one touches no file; otherwise one memoized read answers both
	// the presence and the projection.
	if len(filtered) == 0 {
		return nil, false
	}
	raw, has := s.historyFileViewHas(&s.codexHist, codexFiveHourHistorySpec, filtered, now)
	if !has {
		return nil, false
	}
	return raw, true
}

func (s *planUsageStore) historyFileView(memo *planUsageHistoryMemo, spec planUsageHistorySpec, entries []planUsageEntry, now int64) json.RawMessage {
	raw, _ := s.historyFileViewHas(memo, spec, entries, now)
	return raw
}

// historyFileViewHas is historyFileView, also reporting whether the history
// holds any unexpired sample (false when it is unavailable).
func (s *planUsageStore) historyFileViewHas(memo *planUsageHistoryMemo, spec planUsageHistorySpec, entries []planUsageEntry, now int64) (json.RawMessage, bool) {
	p := planUsageHistoryProjection{SchemaVersion: 1, Status: "empty", Samples: []planUsageSample{}}
	if s == nil {
		raw, _ := json.Marshal(p)
		return raw, false
	}
	key, _ := json.Marshal(entries)
	s.mu.Lock()
	defer s.mu.Unlock()
	h, err := s.readHistoryFileLocked(memo, spec)
	if err != nil {
		p.Status = "unavailable"
		raw, _ := json.Marshal(p)
		return raw, false
	}
	viewKey := s.claudeSlotID + "|" + s.gptSlotID + "|" + string(key)
	if memo.view != nil && memo.viewRev == memo.rev && memo.viewKey == viewKey && now <= memo.viewUntil {
		return memo.view, memo.viewHas
	}
	pruned := prunePlanUsageHistory(h.Records, now)
	until := int64(math.MaxInt64)
	for _, r := range pruned {
		until = min(until, r.Sample.AsOf+planUsageHistoryAge.Milliseconds())
	}
	if len(pruned) > 0 {
		p.Status = "ok"
	}
	var slotFilter map[string]string
	for _, e := range entries {
		histH := historyHarnessFor(e.Harness)
		if id := s.activeSlotForHarnessLocked(histH); id != "" {
			if slotFilter == nil {
				slotFilter = map[string]string{}
			}
			slotFilter[histH] = id
		}
	}
	p.Samples = projectPlanUsageHistoryForSlots(pruned, entries, slotFilter, false)
	raw, err := json.Marshal(p)
	if err != nil {
		raw, _ = json.Marshal(planUsageHistoryProjection{SchemaVersion: 1, Status: "unavailable", Samples: []planUsageSample{}})
		return raw, false
	}
	memo.viewRev, memo.viewKey, memo.viewUntil, memo.view, memo.viewHas = memo.rev, viewKey, until, raw, len(pruned) > 0
	return raw, len(pruned) > 0
}

// projectPlanUsageHistory keeps, per harness/group/kind, only what trend() can
// draw for that group's entry window. trend() draws the samples whose reset
// equals the window's and links each to the sample sorted directly before it
// in the whole harness/group/kind series, so that neighbor is kept even when it
// belongs to another window: dropping it would join samples the full series
// keeps apart. The window's current observation, which trend() inserts
// itself, keeps its neighbor too. An entry may carry several windows of one
// kind, and trend() runs once per window. A group without an entry window is
// never drawn and sends nothing.
func projectPlanUsageHistory(records []planUsageHistoryRecord, entries []planUsageEntry) []planUsageSample {
	return projectPlanUsageHistoryForSlots(records, entries, nil, false)
}

// projectPlanUsageHistoryForSlots projects drawable samples with optional
// per-harness slot filtering. When strictSlot is false (active slot view),
// samples stamped with the active slot or unstamped legacy samples are kept and
// samples stamped for other slots are dropped before neighbor selection. When
// strictSlot is true (inactive slot preview), only samples stamped with the
// requested slot id are kept.
func projectPlanUsageHistoryForSlots(records []planUsageHistoryRecord, entries []planUsageEntry, slotFilter map[string]string, strictSlot bool) []planUsageSample {
	type group struct{ harness, grp, kind string }
	current := map[group][]planUsageSample{}
	for _, e := range entries {
		h := historyHarnessFor(e.Harness)
		for _, w := range e.Windows {
			at := w.AsOf
			if at == 0 {
				at = e.AsOf
			}
			g := group{h, e.Group, w.Kind}
			current[g] = append(current[g], planUsageSample{Harness: h, Group: e.Group, Kind: w.Kind, Source: e.Source, ResetsAt: w.ResetsAt, AsOf: at, Percent: w.Percent})
		}
	}
	series := map[group][]int{}
	for i, r := range records {
		g := group{r.Sample.Harness, r.Sample.Group, r.Sample.Kind}
		if len(current[g]) == 0 {
			continue
		}
		if wantSlot, ok := slotFilter[r.Sample.Harness]; ok && wantSlot != "" {
			if strictSlot {
				if r.Sample.SlotID != wantSlot {
					continue
				}
			} else if r.Sample.SlotID != "" && r.Sample.SlotID != wantSlot {
				continue
			}
		}
		series[g] = append(series[g], i)
	}
	keep := make([]bool, len(records))
	for g, indices := range series {
		sort.Slice(indices, func(a, b int) bool { return usageTrendBefore(records[indices[a]].Sample, records[indices[b]].Sample) })
		for _, cur := range current[g] {
			for n, i := range indices {
				if records[i].Sample.ResetsAt == cur.ResetsAt {
					keep[i] = true
					if n > 0 {
						keep[indices[n-1]] = true
					}
				}
			}
			if n := sort.Search(len(indices), func(n int) bool { return !usageTrendBefore(records[indices[n]].Sample, cur) }); n > 0 {
				keep[indices[n-1]] = true
			}
		}
	}
	out := []planUsageSample{}
	for i, r := range records {
		if keep[i] {
			out = append(out, r.Sample)
		}
	}
	return out
}

// usageTrendBefore must match trend()'s sort in assets/usage.js: asOf, then
// the identity string source|resetsAt|asOf. Sources are fixed ASCII, so byte
// order equals JavaScript's string order.
func usageTrendBefore(a, b planUsageSample) bool {
	if a.AsOf != b.AsOf {
		return a.AsOf < b.AsOf
	}
	return usageTrendIdentity(a) < usageTrendIdentity(b)
}

func usageTrendIdentity(v planUsageSample) string {
	return v.Source + "|" + strconv.FormatInt(v.ResetsAt, 10) + "|" + strconv.FormatInt(v.AsOf, 10)
}

func usageSamples(entry planUsageEntry) []planUsageSample {
	return usageSamplesWithSlot(entry, "")
}

func usageSamplesWithSlot(entry planUsageEntry, slotID string) []planUsageSample {
	samples := make([]planUsageSample, 0, len(entry.Windows))
	for _, w := range entry.Windows {
		samples = append(samples, planUsageSample{Harness: entry.Harness, Group: entry.Group, Kind: w.Kind, Source: entry.Source, SlotID: slotID, ResetsAt: w.ResetsAt, AsOf: w.AsOf, Percent: w.Percent})
	}
	return samples
}

// ValidateLegacyUsageHistory reports whether the usage history reader would
// accept raw as the cache file at rel (plan-usage-history.json,
// plan-usage-codex-history.json or plan-usage-agy-history.json, relative to
// the dashboard directory): schema 1, a samples array, and every sample
// valid for that history with its required members. The daemon metadata
// import uses it as its usage history validator (wsmeta.LegacyValidator; a
// dashboard file has no reply-id), so a history this reader protects from
// append and rewrite stays out of the live store.
func ValidateLegacyUsageHistory(rel, _ string, raw []byte) error {
	name := path.Base(rel)
	if path.Dir(rel) != "." {
		return fmt.Errorf("unknown usage history %q", rel)
	}
	for _, spec := range []planUsageHistorySpec{defaultHistorySpec, codexFiveHourHistorySpec, agyHistorySpec} {
		if spec.cacheName == name {
			_, err := parsePlanUsageHistoryWith(raw, spec)
			return err
		}
	}
	return fmt.Errorf("unknown usage history %q", name)
}
