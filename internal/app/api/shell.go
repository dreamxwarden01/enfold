package api

import "github.com/dreamxwarden01/enfold/internal/app"

// Hooks is what the shell lends the Shell service: window, dialogs, quit.
// The service holds them behind an unexported field so that nothing of
// Wails is reflected into the bound surface.
type Hooks struct {
	ShowWindow  func()
	CloseWindow func()
	// CloseDecided is the close question's answer (APP.md §2.4): "tray" or
	// "quit", with remember writing Settings.CloseAction first. The shell
	// then closes the window its own way — which never asks again — or
	// runs its Quit.
	CloseDecided func(action string, remember bool) *app.Error
	// PickFiles and PickFolder return nil/"" and no error when cancelled.
	PickFiles  func(title string, multiple bool) ([]string, error)
	PickFolder func(title string) (string, error)
	// SaveFile opens the Save dialog; dir, when given, is the folder it
	// opens in (APP.md §6's lastArchiveFolder), empty for the shell's own
	// last place.
	SaveFile func(title, filename, dir string) (string, error)
	// Reveal shows a path in the file manager.
	Reveal func(path string) error
	// Quit runs the shell's quit flow: name any running operations, resolve,
	// then end the process.
	Quit func()
	// DragOut runs the native drag of the selected records out of the
	// window — on a thread of the drag's own — and returns when it has
	// ended (APP.md §3); nil when the shell has no native drag.
	DragOut func(archiveID string, recordIDs []string) (app.DragOutResult, *app.Error)
}

// Shell is the window and the native dialogs.
type Shell struct {
	h Hooks
}

// NewShell builds the service over the shell's hooks.
func NewShell(h Hooks) *Shell { return &Shell{h: h} }

func (s *Shell) ShowWindow()  { s.h.ShowWindow() }
func (s *Shell) CloseWindow() { s.h.CloseWindow() }
func (s *Shell) Quit()        { s.h.Quit() }

// CloseDecided answers the close question the page asked when the window's
// close was cancelled (APP.md §2.4, ruled 2026-09-13). action is "tray" or
// "quit" — never "ask", which is what was being asked — and remember
// writes it to Settings.CloseAction through the core, exactly as the
// settings page's own save does, so the next close does not ask. The
// window then goes to the tray, leaving every page, or the shell's Quit
// runs.
func (s *Shell) CloseDecided(action string, remember bool) error {
	if action != app.CloseTray && action != app.CloseQuit {
		return &app.Error{Code: app.CodeParams}
	}
	if s.h.CloseDecided == nil {
		return &app.Error{Code: app.CodeInternal}
	}
	return asErr(s.h.CloseDecided(action, remember))
}

func (s *Shell) PickFiles(title string, multiple bool) ([]string, error) {
	p, err := s.h.PickFiles(title, multiple)
	if err != nil {
		return nil, &app.Error{Code: app.CodeInternal}
	}
	if p == nil {
		p = []string{}
	}
	return p, nil
}

func (s *Shell) PickFolder(title string) (string, error) {
	p, err := s.h.PickFolder(title)
	if err != nil {
		return "", &app.Error{Code: app.CodeInternal}
	}
	return p, nil
}

func (s *Shell) SaveFile(title, filename, dir string) (string, error) {
	p, err := s.h.SaveFile(title, filename, dir)
	if err != nil {
		return "", &app.Error{Code: app.CodeInternal}
	}
	return p, nil
}

// Reveal shows a path in the file manager. The page prints on its own: the
// recovery key's window.print() is watched by nothing since 2026-09-09, and
// pressing Print… counts as done (APP.md §6).
func (s *Shell) Reveal(path string) error {
	if err := s.h.Reveal(path); err != nil {
		return &app.Error{Code: app.CodeIO}
	}
	return nil
}

// DragOut is the one gesture of APP.md §3: a press-and-move over selected
// rows starts one native drag of those records out of the window, and this
// call blocks until DoDragDrop returns — the window stays alive and its
// page keeps moving meanwhile, the drag having a thread of its own and the
// main thread being nobody's to hold (ruled 2026-09-11). The staged copy is
// extracted by the drop's own request under an operation of kind dragout,
// which the strip follows (preparing, then awaiting) and which ends when
// the drag reports how it ended (OpView.DragResult). A release over
// Enfold's own window is a self-drop: nothing is extracted, and the page —
// receiving the WebView's drop with paths under Folder — performs the Move
// of the ids it kept in flight. drag.unsupported when no native drag can
// run, drag.busy while one is running, params for an empty or root
// selection, and the archive's own codes.
func (s *Shell) DragOut(archiveID string, recordIDs []string) (app.DragOutResult, error) {
	if s.h.DragOut == nil {
		return app.DragOutResult{}, &app.Error{Code: app.CodeDragUnsupported}
	}
	r, e := s.h.DragOut(archiveID, recordIDs)
	return r, asErr(e)
}
