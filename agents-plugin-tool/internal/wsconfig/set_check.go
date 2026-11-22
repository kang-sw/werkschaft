package wsconfig

import "maps"

// SetCheck validates the overrides one config file would hold after a write,
// for keys that only make sense as a set. A non-nil error refuses the write.
type SetCheck func(overrides map[string]string) error

var setChecks = map[string][]SetCheck{}

// RegisterSetCheck runs check before any scalar write or reset of one of keys
// commits, against the file's overrides as the write would leave them. Call
// at package init time.
func RegisterSetCheck(keys []string, check SetCheck) {
	for _, key := range keys {
		setChecks[key] = append(setChecks[key], check)
	}
}

// checkSet runs the set checks registered for itemKey against the
// would-be overrides.
func checkSet(itemKey string, overrides map[string]string) error {
	checks := setChecks[itemKey]
	if len(checks) == 0 {
		return nil
	}
	snapshot := maps.Clone(overrides)
	if snapshot == nil {
		snapshot = map[string]string{}
	}
	for _, check := range checks {
		if err := check(snapshot); err != nil {
			return err
		}
	}
	return nil
}
