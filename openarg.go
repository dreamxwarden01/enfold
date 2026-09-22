// The file a launch was asked to open (docs/APP.md §14): Explorer's
// double-click on an `.efd`, an *Open with*, a path typed on the command
// line. The parsing is here, apart from the Wails wiring and apart from
// Windows, so that it is a function with an answer rather than a side
// effect of starting up.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// afterProgram drops the program's own path from a launch's arguments.
// os.Args[0] is the executable, and SecondInstanceData.Args is the whole
// os.Args of the launch that handed over — Wails' notifyFirstInstance
// sends it as it stands — so the program's own path is in there too, and
// it is a file that exists: passed on, the second launch of Enfold would
// try to open Enfold.
func afterProgram(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	return args[1:]
}

// openArguments is what a launch named, in the order it named it: every
// argument that is a file that is there, flags passed over, a relative
// path resolved against dir — the process's own working directory at
// launch, `SecondInstanceData.WorkingDir` for a second launch — and every
// answer absolute, since the core's working directory is nobody's to
// guess from (Archives.OpenPath refuses a relative path).
//
// The caller opens the first and counts the rest (APP.md §14: "the first
// is opened, the rest are named in a toast"). A folder is not a file to
// open and is passed over with the rest; nothing here reads a byte of
// anything, and whether a file is an archive at all is the core's word,
// from its envelope.
func openArguments(args []string, dir string) []string {
	var found []string
	for _, a := range args {
		// Enfold takes no flags. One that turns up is not a path, and a
		// leading dash is what every convention it could follow uses.
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		p := a
		if !filepath.IsAbs(p) && dir != "" {
			p = filepath.Join(dir, p)
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			continue
		}
		found = append(found, p)
	}
	return found
}

// pendingOpen is the one slot a launch's request waits in.
//
// **Why a slot and not only an event** (the boot order of APP.md §7 and
// §2.4): the page subscribes to every event at module scope before its
// first `Status()`, but there is no page at all until the window has
// drawn one, and a launch argument exists before the window is asked for.
// An event emitted at launch would be emitted into nothing. So the launch
// argument waits here and the page comes for it with `Shell.PendingOpen()`
// as it boots, which cannot be lost however slowly the WebView starts. A
// second launch, whose page is already up and listening, is told by
// `shell.open` — and stages its request here as well, since it may be the
// launch that recreates a window closed to the tray, whose fresh page
// would never hear the event either.
//
// Seq numbers the requests from one and the take empties the slot, so the
// page opens each of them once however it reached it.
type pendingOpen struct {
	mu  sync.Mutex
	seq int
	req *app.OpenRequest
}

// stage puts a launch's paths in the slot and answers the request made of
// them, nil when the launch named no file.
//
// A launch arriving while one is still waiting **merges** into it rather
// than replacing it (the review's finding 2): two double-clicks before
// the window has drawn are two files the user asked for, and dropping the
// first would lose the one they asked for first and say nothing about it.
// So the earlier first path stays the one that opens, the newcomer's
// paths join the rest — which the page names in its toast — and the seq
// advances, since this is a request the page has not seen. Duplicates are
// dropped: the same file double-clicked twice is one file.
func (p *pendingOpen) stage(paths []string) *app.OpenRequest {
	if len(paths) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	req := app.OpenRequest{Seq: p.seq}
	var rest []string
	if p.req != nil {
		req.Path = p.req.Path
		rest = append(append(rest, p.req.Rest...), paths...)
	} else {
		req.Path = paths[0]
		rest = append(rest, paths[1:]...)
	}
	req.Rest = withoutDuplicates(rest, req.Path)
	p.req = &req
	return &req
}

// withoutDuplicates keeps the first of each path and drops any that is
// already the request's own first path. The paths come from
// openArguments, which made every one of them absolute and clean the same
// way, so they are compared as they stand: two spellings of one file
// would have to be typed by hand to differ here, and naming a file twice
// is the worse outcome of the two.
func withoutDuplicates(paths []string, first string) []string {
	seen := map[string]bool{first: true}
	var out []string
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// take is what Shell.PendingOpen answers: the request waiting, and the
// slot emptied. Seq 0 says there was none.
func (p *pendingOpen) take() app.OpenRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.req == nil {
		return app.OpenRequest{}
	}
	req := *p.req
	p.req = nil
	return req
}
