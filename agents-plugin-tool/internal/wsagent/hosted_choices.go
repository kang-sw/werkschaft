package wsagent

// sessionChoiceLister bypasses Support's fallback cache as soon as a native
// session reports its authoritative choices. It does no I/O.
type sessionChoiceLister interface {
	sessionChoices() ([]ModelChoice, []string)
}
