package werk

import "github.com/kang-sw/devenv/internal/wscompact"

const rereadBytes = wscompact.RereadBytes

func (a *Agent) compactRereads(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var files map[string]string
	if a.cfg.RereadFiles != nil {
		files = a.cfg.RereadFiles(paths, rereadBytes)
	}
	return wscompact.FormatRereads(paths, files, a.cfg.SummarySecrets...)
}
