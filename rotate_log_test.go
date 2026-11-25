package inscribe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateLog(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "inscribe.log")
	if err := os.WriteFile(path, []byte("small\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rotateLog(home, 100)
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("a small log was rotated")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 101)), 0o600); err != nil {
		t.Fatal(err)
	}
	rotateLog(home, 100)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("inscribe.log still there after rotation")
	}
	if b, err := os.ReadFile(path + ".1"); err != nil || len(b) != 101 {
		t.Fatalf("inscribe.log.1 = %d bytes, %v", len(b), err)
	}
	appendLog(home, "next")
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "next") {
		t.Fatal("logging did not start a new file")
	}
}
