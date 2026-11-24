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
		"CREATE_SUSPENDED = 0x00000004",
		"PROCESS_POWER_THROTTLING = 4",
		"JOB_OBJECT_ASSOCIATE_COMPLETION_PORT_INFORMATION = 7",
		"JOB_OBJECT_MSG_NEW_PROCESS = 6",
		"def _clear_windows_eco_qos(",
		"class _WindowsDescendantQoSJob:",
		"SetPriorityClass",
		"SetProcessInformation",
		"PROCESS_POWER_THROTTLING_STATE",
		"CreateJobObjectW",
		"CreateIoCompletionPort",
		"SetInformationJobObject",
		"AssignProcessToJobObject",
		"GetQueuedCompletionStatus",
		"PostQueuedCompletionStatus",
		"ResumeThread",
		"subprocess.CREATE_NEW_PROCESS_GROUP | NORMAL_PRIORITY_CLASS",
		"0x00080000 | 0x00000400 | NORMAL_PRIORITY_CLASS | CREATE_SUSPENDED",
		"_clear_windows_eco_qos(info.hProcess",
	}
	for _, needle := range required {
		if !strings.Contains(src, needle) {
			t.Errorf("embedded holder.py missing %q", needle)
		}
	}
	forbidden := []string{
		"JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE",
		"JOB_OBJECT_LIMIT_BREAKAWAY_OK",
		"SetProcessAffinityMask",
	}
	for _, needle := range forbidden {
		if strings.Contains(src, needle) {
			t.Errorf("embedded holder.py must not contain %q", needle)
		}
	}
}
