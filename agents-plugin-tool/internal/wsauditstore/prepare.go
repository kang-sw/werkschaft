package wsauditstore

import (
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsredact"
)

// MetaTruncated is the meta key of the marker the store writes when optional
// meta entries were dropped to keep a record's metadata within
// MetadataBound. Its value is {"dropped_keys": n, "dropped_bytes": b}.
const MetaTruncated = "meta_truncated"

// requiredMetaKeys are meta keys readers depend on; the metadata bound keeps
// them before any optional entry (Decision 3, (2138)).
var requiredMetaKeys = []string{wsaudit.MetaEstimated, wsaudit.MetaSignatureOnly}

// prepare turns a captured envelope into the record the store persists:
// identifiers normalized, every body redacted in full, model-seen bounds
// carried through masking, each body capped to head and tail, and the
// metadata bounded. Store-owned fields (redactions, stored_trunc) are
// recomputed.
func prepare(ev wsaudit.Envelope, lim Limits) (wsaudit.Envelope, error) {
	if ev.V == 0 {
		ev.V = wsaudit.SchemaVersion
	}
	wsaudit.NormalizeIDs(&ev)
	ev.Redactions = nil
	ev.StoredTrunc = nil
	var seen wsaudit.SeenBounds
	if ev.Seen != nil {
		seen = *ev.Seen
	}
	var trunc wsaudit.StoredTrunc
	var err error
	ev.In, seen.In, trunc.In, ev.Redactions = prepareBody(wsaudit.FieldIn, ev.In, seen.In, lim.BodyBytes, ev.Redactions)
	ev.Out, seen.Out, trunc.Out, ev.Redactions = prepareBody(wsaudit.FieldOut, ev.Out, seen.Out, lim.BodyBytes, ev.Redactions)
	ev.Raw, _, trunc.Raw, ev.Redactions = prepareBody(wsaudit.FieldRaw, ev.Raw, nil, lim.BodyBytes, ev.Redactions)
	ev.Seen = nil
	if !seen.IsEmpty() {
		ev.Seen = &seen
	}
	if !trunc.IsEmpty() {
		ev.StoredTrunc = &trunc
	}
	if err := ev.Validate(); err != nil {
		return wsaudit.Envelope{}, err
	}
	if ev, err = boundMetadata(ev); err != nil {
		return wsaudit.Envelope{}, err
	}
	return ev, nil
}

// prepareBody redacts body in full, maps its seen bound into the redacted
// body, then applies the per-body cap and maps the bound into the stored
// body. Redaction offsets stay in the redacted body before the cap.
func prepareBody(field, body string, seen *int, capBytes int64, reds []wsaudit.Redaction) (string, *int, *wsaudit.BodyTrunc, []wsaudit.Redaction) {
	if body == "" {
		return "", nil, nil, reds
	}
	masked, matches := wsredact.Redact(body)
	for _, m := range matches {
		if m.Length > 0 {
			reds = append(reds, wsaudit.Redaction{Field: field, Offset: m.Offset, Rule: m.Rule, Length: m.Length})
		}
	}
	bound := -1
	if seen != nil {
		bound = wsredact.MapOffset(matches, min(max(*seen, 0), len(body)))
	}
	stored, trunc := capBody(masked, capBytes)
	if bound >= 0 && trunc != nil {
		omitted := trunc.OriginalBytes - trunc.HeadBytes - trunc.TailBytes
		switch {
		case bound <= trunc.HeadBytes:
		case bound >= trunc.OriginalBytes-trunc.TailBytes:
			bound -= omitted
		default:
			// The model's cut fell in the omitted middle: everything stored
			// before the cut point was seen, nothing after.
			bound = trunc.HeadBytes
		}
	}
	if bound < 0 || bound >= len(stored) {
		return stored, nil, trunc, reds
	}
	return stored, &bound, trunc, reds
}

// capBody keeps body whole when its encoded length fits capBytes, otherwise
// its longest head and tail whose encoded lengths fit each half of the cap,
// cut on rune boundaries.
func capBody(body string, capBytes int64) (string, *wsaudit.BodyTrunc) {
	if int64(wsaudit.EncodedLen(body)) <= capBytes {
		return body, nil
	}
	headShare := int(capBytes / 2)
	tailShare := int(capBytes) - headShare
	head := 0
	used := 0
	for head < len(body) {
		w, size := wsaudit.EncodedRuneLen(body[head:])
		if used+w > headShare {
			break
		}
		used += w
		head += size
	}
	tail := len(body)
	used = 0
	for tail > head {
		_, size := utf8.DecodeLastRuneInString(body[:tail])
		w, _ := wsaudit.EncodedRuneLen(body[tail-size:])
		if used+w > tailShare {
			break
		}
		used += w
		tail -= size
	}
	for tail < len(body) && wsaudit.EncodedLen(body[tail:]) > tailShare {
		_, size := utf8.DecodeRuneInString(body[tail:])
		tail += size
	}
	return body[:head] + body[tail:], &wsaudit.BodyTrunc{OriginalBytes: len(body), HeadBytes: head, TailBytes: len(body) - tail}
}

// metadataSize is the encoded size of ev without its three bodies.
func metadataSize(ev wsaudit.Envelope) (int, error) {
	ev.In, ev.Out, ev.Raw = "", "", ""
	raw, err := json.Marshal(ev)
	if err != nil {
		return 0, err
	}
	return len(raw), nil
}

// boundMetadata keeps the encoded metadata within MetadataBound: redaction
// entries stay in offset order up to the bound and the rest collapse to one
// summary per (field, rule); then required meta keys are reserved and the
// optional ones kept in key order, the dropped ones counted in one marker.
func boundMetadata(ev wsaudit.Envelope) (wsaudit.Envelope, error) {
	size, err := metadataSize(ev)
	if err != nil || size <= int(MetadataBound) {
		return ev, err
	}
	if len(ev.Redactions) > 0 {
		all := ev.Redactions
		try := func(k int) (wsaudit.Envelope, int, error) {
			cand := ev
			cand.Redactions = append(append([]wsaudit.Redaction(nil), all[:k]...), summarize(all[k:])...)
			n, err := metadataSize(cand)
			return cand, n, err
		}
		lo, hi := 0, len(all)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			_, n, err := try(mid)
			if err != nil {
				return ev, err
			}
			if n <= int(MetadataBound) {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		for k := lo; k >= 0; k-- {
			cand, n, err := try(k)
			if err != nil {
				return ev, err
			}
			ev, size = cand, n
			if n <= int(MetadataBound) {
				break
			}
		}
		if size <= int(MetadataBound) {
			return ev, nil
		}
	}
	if len(ev.Meta) > 0 {
		if ev, err = boundMeta(ev); err != nil {
			return ev, err
		}
		if size, err = metadataSize(ev); err != nil {
			return ev, err
		}
	}
	if size > int(MetadataBound) {
		return ev, fmt.Errorf("wsauditstore: record metadata is %d bytes after bounding (limit %d)", size, MetadataBound)
	}
	return ev, nil
}

// summarize collapses redaction entries into one summary per (field, rule):
// the first offset, the count, and the total original length.
func summarize(rest []wsaudit.Redaction) []wsaudit.Redaction {
	if len(rest) == 0 {
		return nil
	}
	type key struct{ field, rule string }
	index := map[key]int{}
	var out []wsaudit.Redaction
	for _, r := range rest {
		k := key{r.Field, r.Rule}
		count := max(r.Count, 1)
		if i, ok := index[k]; ok {
			out[i].Count += count
			out[i].Length += r.Length
			continue
		}
		index[k] = len(out)
		out = append(out, wsaudit.Redaction{Field: r.Field, Offset: r.Offset, Rule: r.Rule, Length: r.Length, Count: count})
	}
	order := map[string]int{wsaudit.FieldIn: 0, wsaudit.FieldOut: 1, wsaudit.FieldRaw: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if order[out[i].Field] != order[out[j].Field] {
			return order[out[i].Field] < order[out[j].Field]
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// boundMeta reserves required meta keys, keeps optional entries in key order
// while the metadata fits, and records the dropped ones in one marker.
func boundMeta(ev wsaudit.Envelope) (wsaudit.Envelope, error) {
	kept := map[string]any{}
	for _, key := range requiredMetaKeys {
		if v, ok := ev.Meta[key].(bool); ok {
			kept[key] = v
		}
	}
	var optional []string
	for key := range ev.Meta {
		if _, req := kept[key]; !req && key != MetaTruncated {
			optional = append(optional, key)
		}
	}
	sort.Strings(optional)
	sizes := make([]int, len(optional))
	totalBytes := 0
	for i, key := range optional {
		k, err := json.Marshal(key)
		if err != nil {
			return ev, err
		}
		v, err := json.Marshal(ev.Meta[key])
		if err != nil {
			return ev, err
		}
		sizes[i] = len(k) + 1 + len(v) + 1
		totalBytes += sizes[i]
	}
	// Size the base with a marker as wide as it can get.
	base := ev
	base.Meta = cloneMeta(kept)
	base.Meta[MetaTruncated] = map[string]any{"dropped_keys": len(optional), "dropped_bytes": totalBytes}
	used, err := metadataSize(base)
	if err != nil {
		return ev, err
	}
	keep := 0
	for keep < len(optional) && used+sizes[keep] <= int(MetadataBound) {
		used += sizes[keep]
		keep++
	}
	for {
		out := ev
		out.Meta = cloneMeta(kept)
		dropped, droppedBytes := 0, 0
		for i, key := range optional {
			if i < keep {
				out.Meta[key] = ev.Meta[key]
			} else {
				dropped++
				droppedBytes += sizes[i]
			}
		}
		if dropped > 0 {
			out.Meta[MetaTruncated] = map[string]any{"dropped_keys": dropped, "dropped_bytes": droppedBytes}
		}
		n, err := metadataSize(out)
		if err != nil {
			return ev, err
		}
		if n <= int(MetadataBound) || keep == 0 {
			return out, nil
		}
		keep--
	}
}

func cloneMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
