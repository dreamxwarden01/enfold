package api

import "github.com/dreamxwarden01/enfold/internal/app"

// Hooks is what the shell lends the Shell service: window, dialogs, quit.
// The service holds them behind an unexported field so that nothing of
// Wails is reflected into the bound surface.
type Hooks struct {
	ShowWindow  func()
	CloseWindow func()
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
