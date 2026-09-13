//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWerExclusionNameFitsMaxPath: WerAddExcludedApplication takes at most
// MAX_PATH characters and fails rather than truncating, so an installation
// deep enough to pass that would leave the exclusion silently absent. The
// base name is what goes instead, and the choice is made on UTF-16 units,
// which is what the API counts.
func TestWerExclusionNameFitsMaxPath(t *testing.T) {
	short := `C:\Program Files\Enfold\enfold.exe`
	if got, full := werExclusionName(short); got != short || !full {
		t.Fatalf("werExclusionName(%q) = %q, %v; want the path itself", short, got, full)
	}

	// Exactly at the last length the call is offered, and one past it.
	const dir = `C:\`
	const leaf = `enfold.exe`
	pad := func(n int) string {
		// <dir><n padding characters>\<leaf>
		return dir + strings.Repeat("a", n) + `\` + leaf
	}
	fits := maxPath - 2 // one under MAX_PATH - 1, the last length taken whole
	p := pad(fits - len(dir) - 1 - len(leaf))
	if len(p) != fits {
		t.Fatalf("the fixture is %d characters, want %d", len(p), fits)
	}
	if got, full := werExclusionName(p); got != p || !full {
		t.Fatalf("a path of %d characters was not passed whole: %q, %v", len(p), got, full)
	}
	over := pad(fits - len(dir) - len(leaf)) // one longer
	if len(over) != fits+1 {
		t.Fatalf("the fixture is %d characters, want %d", len(over), fits+1)
	}
	if got, full := werExclusionName(over); got != leaf || full {
		t.Fatalf("a path of %d characters gave %q, %v; want the base name", len(over), got, full)
	}

	// A deep install path, which is what this is for.
	deep := `C:\Users\someone\AppData\Local\Programs\` + strings.Repeat(`a-rather-long-folder-name\`, 12) + leaf
	if len(deep) <= maxPath {
		t.Fatalf("the deep fixture is only %d characters", len(deep))
	}
	got, full := werExclusionName(deep)
	if full || got != leaf {
		t.Fatalf("a deep path gave %q, %v; want the base name", got, full)
	}
	if filepath.Base(deep) != got {
		t.Fatalf("the base name is %q, not %q", filepath.Base(deep), got)
	}

	// The count is in UTF-16 units, not bytes: a path of characters that
	// take three bytes each is well under the limit the API applies.
	wide := `C:\` + strings.Repeat("\u4e2d", 100) + `\` + leaf
	if len(wide) <= maxPath {
		t.Fatalf("the wide fixture is only %d bytes, so it proves nothing", len(wide))
	}
	if got, full := werExclusionName(wide); got != wide || !full {
		t.Fatalf("a path of %d bytes but few characters was cut back to %q, %v", len(wide), got, full)
	}
}
