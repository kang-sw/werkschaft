package wsagent

// agy_compat_test.go keeps the agy-named identifiers the existing tests use
// after the shared ACP core moved out of agy.go under acp* names, so those
// test files stay unchanged. Production code carries none of these aliases.

type (
	agyMapper   = acpMapper
	agyProcess  = acpProcess
	agyProtocol = acpProtocol
	agyMessage  = acpMessage
)

const (
	agyKindInit    = acpKindInit
	agyKindNew     = acpKindNew
	agyKindResume  = acpKindResume
	agyKindClose   = acpKindClose
	agyKindConfig  = acpKindConfig
	agyKindMode    = acpKindMode
	agyKindPrompt  = acpKindPrompt
	agyRequestMark = acpRequestMark
)

var (
	agyIDKind    = acpIDKind
	agyAuditBody = acpAuditBody
)
