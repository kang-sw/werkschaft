package wsholder

import (
	"strings"
	"testing"
)

// TestEmbeddedHolderScriptWindowsPriorityQoSParity verifies that the embedded
// holder.py contains the Windows priority + EcoQoS opt-out parity with macOS
// across lift_qos_default, PipeChild, and ConPtyChild.
func TestEmbeddedHolderScriptWindowsPriorityQoSParity(t *testing.T) {
	src := string(script)
	required := []string{
		"NORMAL_PRIORITY_CLASS = 0x00000020",
		"PROCESS_POWER_THROTTLING = 4",
		"def _clear_windows_eco_qos(",
		"SetPriorityClass",
		"SetProcessInformation",
		"PROCESS_POWER_THROTTLING_STATE",
		"subprocess.CREATE_NEW_PROCESS_GROUP | NORMAL_PRIORITY_CLASS",
		"0x00080000 | 0x00000400 | NORMAL_PRIORITY_CLASS",
		"_clear_windows_eco_qos(info.hProcess",
	}
	for _, needle := range required {
		if !strings.Contains(src, needle) {
			t.Errorf("embedded holder.py missing %q", needle)
		}
	}
}
