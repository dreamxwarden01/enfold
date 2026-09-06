package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShellNeverEnablesDebugLogging is the guard of APP.md §1: bound calls
// are stringified before any log-level check, so the shell must never set a
// logger or a level. The source is the evidence.
func TestShellNeverEnablesDebugLogging(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"slog.LevelDebug", "application.DefaultLogger", "LogLevel:", "Logger:"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %q", f, bad)
			}
		}
	}
}
