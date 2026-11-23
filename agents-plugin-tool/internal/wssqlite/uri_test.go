package wssqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFileURIOpensLiteralPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow '?' in a directory name")
	}
	dir := filepath.Join(t.TempDir(), "state?copy#1 %41")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "store.db")
	db, err := sql.Open("sqlite", FileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE example (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestFileURIKeepsWindowsExtendedPath(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{"drive", `\\?\C:\state\store.db`, "file:%5C%5C%3F%5CC:%5Cstate%5Cstore.db"},
		{"UNC", `\\?\UNC\server\share\store.db`, "file:%5C%5C%3F%5CUNC%5Cserver%5Cshare%5Cstore.db"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := fileURI(tt.path); got != tt.want {
				t.Errorf("fileURI(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
