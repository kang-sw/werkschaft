package wsagent

import (
	"errors"
	"io/fs"
	"testing"
)

func TestIsRecordMiss(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	boom := func() bool { t.Fatal("dirExists must not run"); return false }
	if !isRecordMiss(fs.ErrNotExist, true, yes) {
		t.Error("not-found with live dir inside budget must retry")
	}
	if isRecordMiss(fs.ErrNotExist, true, no) {
		t.Error("not-found with deleted dir must not retry")
	}
	if isRecordMiss(fs.ErrNotExist, false, boom) {
		t.Error("not-found past budget must not retry")
	}
	if isRecordMiss(errors.New("other"), true, boom) {
		t.Error("non-not-found error must not retry")
	}
}
