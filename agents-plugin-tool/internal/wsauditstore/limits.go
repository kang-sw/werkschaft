package wsauditstore

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

const (
	kib = int64(1) << 10
	mib = int64(1) << 20

	// MetadataBound bounds the encoded bytes of everything in a stored record
	// outside its three bodies (Decision 3, (2129)=a).
	MetadataBound = 64 * kib
	// MinEffectiveSessionBytes is the floor of the effective per-session cap.
	MinEffectiveSessionBytes = 4 * mib
	// MinBodyBytes is the smallest per-body cap, so a capped body keeps a
	// readable head and tail.
	MinBodyBytes = 4 * kib
	// MinRetention is the shortest retention period.
	MinRetention = 24 * time.Hour
	// MaxRetentionDays is the longest retention a stored day count can
	// express; larger values saturate to it.
	MaxRetentionDays = int64(math.MaxInt64 / int64(24*time.Hour))

	// recordFraming is slack for one record's framing outside the body text
	// and the metadata bound: the line wrapper, body keys and quotes, and the
	// newline.
	recordFraming = 4 * kib
	// HeaderBound is the room a segment keeps for its header line beside one
	// maximal record; the header's lost-identity list folds at lostBudget.
	HeaderBound = 64 * kib
	// preferredSegmentBytes is the segment size when the caps allow it.
	preferredSegmentBytes = 16 * mib
)

// Config keys of the four audit knobs. They are global-only daemon settings
// declared in the dashboard config manifests.
const (
	KeySessionCapMiB = "dashboard.audit_session_cap_mib"
	KeyTotalCapMiB   = "dashboard.audit_total_cap_mib"
	KeyBodyCapKiB    = "dashboard.audit_body_cap_kib"
	KeyRetentionDays = "dashboard.audit_retention_days"
)

// Keys lists the audit knob config keys.
func Keys() []string {
	return []string{KeySessionCapMiB, KeyTotalCapMiB, KeyBodyCapKiB, KeyRetentionDays}
}

// Limits are the audit store's byte caps and retention period.
type Limits struct {
	// SessionBytes is the per-session byte cap; the oldest whole segment
	// goes first.
	SessionBytes int64
	// TotalBytes is the per-store byte cap; whole sessions' rings go in
	// least-recently-used order.
	TotalBytes int64
	// BodyBytes caps each stored body's encoded bytes; a longer body keeps
	// its head and tail halves.
	BodyBytes int64
	// Retention removes a session's ring this long after it was last seen.
	Retention time.Duration
}

// DefaultLimits are the built-in knob values: 256 MiB per session, 4 GiB per
// store, 1 MiB per body, 7 days after last seen.
func DefaultLimits() Limits {
	return Limits{SessionBytes: 256 * mib, TotalBytes: 4096 * mib, BodyBytes: 1024 * kib, Retention: 7 * 24 * time.Hour}
}

// EffectiveSessionBytes is min(per-session cap, store total).
func (l Limits) EffectiveSessionBytes() int64 {
	return min(l.SessionBytes, l.TotalBytes)
}

// MaxRecordBytes bounds one complete encoded record line: three capped
// bodies, the metadata bound, and framing.
func (l Limits) MaxRecordBytes() int64 {
	return 3*l.BodyBytes + MetadataBound + recordFraming
}

// SegmentBytes is the segment rotation size: at most a quarter of the
// effective per-session cap and never smaller than one maximal record beside
// a maximal header, so every set Clamp returns also passes Validate.
func (l Limits) SegmentBytes() int64 {
	return max(min(preferredSegmentBytes, l.EffectiveSessionBytes()/4), l.MaxRecordBytes()+HeaderBound)
}

// Validate reports why the knobs do not hold as a set: the effective
// per-session cap is at least 4 MiB, the per-body cap is at most 1/16 of it,
// so one maximal record always fits a segment of at most a quarter of it.
func (l Limits) Validate() error {
	switch {
	case l.SessionBytes < MinEffectiveSessionBytes:
		return fmt.Errorf("audit per-session cap %d MiB is below the %d MiB floor", l.SessionBytes/mib, MinEffectiveSessionBytes/mib)
	case l.TotalBytes < MinEffectiveSessionBytes:
		return fmt.Errorf("audit store total cap %d MiB is below the %d MiB floor", l.TotalBytes/mib, MinEffectiveSessionBytes/mib)
	case l.BodyBytes < MinBodyBytes:
		return fmt.Errorf("audit per-body cap %d KiB is below the %d KiB floor", l.BodyBytes/kib, MinBodyBytes/kib)
	case l.BodyBytes > l.EffectiveSessionBytes()/16:
		return fmt.Errorf("audit per-body cap %d KiB exceeds 1/16 of the effective per-session cap (%d KiB)", l.BodyBytes/kib, l.EffectiveSessionBytes()/16/kib)
	case l.Retention < MinRetention:
		return fmt.Errorf("audit retention %s is below one day", l.Retention)
	}
	if l.MaxRecordBytes()+HeaderBound > l.SegmentBytes() || l.SegmentBytes() > l.EffectiveSessionBytes()/4 {
		return fmt.Errorf("audit caps leave no segment that fits one record")
	}
	return nil
}

// Clamp moves each knob to the nearest valid value and reports one warning
// per change. Clamp(l) of a valid l returns it unchanged with no warnings.
func (l Limits) Clamp() (Limits, []string) {
	var warnings []string
	out := l
	if out.SessionBytes < MinEffectiveSessionBytes {
		out.SessionBytes = MinEffectiveSessionBytes
		warnings = append(warnings, fmt.Sprintf("%s raised to the %d MiB floor", KeySessionCapMiB, MinEffectiveSessionBytes/mib))
	}
	if out.TotalBytes < MinEffectiveSessionBytes {
		out.TotalBytes = MinEffectiveSessionBytes
		warnings = append(warnings, fmt.Sprintf("%s raised to the %d MiB floor", KeyTotalCapMiB, MinEffectiveSessionBytes/mib))
	}
	if out.BodyBytes < MinBodyBytes {
		out.BodyBytes = MinBodyBytes
		warnings = append(warnings, fmt.Sprintf("%s raised to the %d KiB floor", KeyBodyCapKiB, MinBodyBytes/kib))
	}
	if ceiling := out.EffectiveSessionBytes() / 16 / kib * kib; out.BodyBytes > ceiling {
		out.BodyBytes = ceiling
		warnings = append(warnings, fmt.Sprintf("%s lowered to %d KiB, 1/16 of the effective per-session cap", KeyBodyCapKiB, ceiling/kib))
	}
	if out.Retention < MinRetention {
		out.Retention = MinRetention
		warnings = append(warnings, fmt.Sprintf("%s raised to 1 day", KeyRetentionDays))
	}
	return out, warnings
}

// LimitsFromValues builds Limits from the knobs' stored config strings,
// keyed by config key. A missing or unparsable value takes its default.
func LimitsFromValues(values map[string]string) (Limits, []string) {
	l := DefaultLimits()
	var warnings []string
	read := func(key string, unit int64, dst *int64) {
		raw, ok := values[key]
		if !ok || raw == "" {
			return
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 || n > (1<<62)/unit {
			warnings = append(warnings, fmt.Sprintf("%s value %q is not a valid integer; using the default", key, raw))
			return
		}
		*dst = n * unit
	}
	read(KeySessionCapMiB, mib, &l.SessionBytes)
	read(KeyTotalCapMiB, mib, &l.TotalBytes)
	read(KeyBodyCapKiB, kib, &l.BodyBytes)
	days := int64(l.Retention / (24 * time.Hour))
	read(KeyRetentionDays, 1, &days)
	// A day count past the time.Duration range saturates: a huge value means
	// "keep (almost) forever", never a wrapped negative period.
	days = min(days, MaxRetentionDays)
	l.Retention = time.Duration(days) * 24 * time.Hour
	return l, warnings
}

// ValidateValues checks a would-be set of stored knob values, as a config
// write leaves them, and refuses one that breaks the set.
func ValidateValues(values map[string]string) error {
	l, warnings := LimitsFromValues(values)
	if len(warnings) > 0 {
		return fmt.Errorf("%s", warnings[0])
	}
	return l.Validate()
}
