package app

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// previewServer is the loopback transport of APP.md §4: bound once per
// process, one archive.Reader per request under http.ServeContent,
// no-store, single-range only, per-archive tokens. It outlives the
// session; it serves only archives the core holds open.
type previewServer struct {
	c         *Core
	ln        net.Listener
	srv       *http.Server
	port      int
	mu        sync.Mutex
	oneTime   map[string]oneTimeSecret // one-shot secrets for the recovery-key display
	appOrigin string
}

type oneTimeSecret struct {
	value   string
	label   string // the way in the value belongs to, for the saved file
	id      string // the recovery key's ID (FORMAT.md §18.4), for the saved file
	expires time.Time
	fetched bool // the URL was consumed; the value stays behind the handle for a save (APP.md §3 Keys)
}

func startPreview(c *Core) (*previewServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &previewServer{c: c, ln: ln, port: ln.Addr().(*net.TCPAddr).Port, oneTime: map[string]oneTimeSecret{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/p/", p.serveFile)
	mux.HandleFunc("/s/", p.serveSecret)
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go p.srv.Serve(ln)
	return p, nil
}

func (p *previewServer) stop() {
	p.srv.Close()
}

// SetAppOrigin tells the server which page origin may fetch the one-time
// secrets (CORS pinned, never *).
func (c *Core) SetAppOrigin(origin string) {
	if c.preview != nil {
		c.preview.mu.Lock()
		c.preview.appOrigin = origin
		c.preview.mu.Unlock()
	}
}

func (p *previewServer) url(token string, fileID [16]byte) string {
	return fmt.Sprintf("http://127.0.0.1:%d/p/%s/%s", p.port, token, hexID(fileID))
}

// Port is the bound port, for the page's CSP.
func (c *Core) PreviewPort() int {
	if c.preview == nil {
		return 0
	}
	return c.preview.port
}

func noStore(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

// serveFile: GET /p/<archive-token>/<fileID>.
func (p *previewServer) serveFile(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/p/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	token, fidHex := parts[0], parts[1]
	fid, ok := parseID(fidHex)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Multi-range requests are refused so the Reader may be closed on
	// return (internal/stream documents why).
	if rg := r.Header.Get("Range"); strings.Contains(rg, ",") {
		w.Header().Set("Content-Range", "bytes */*")
		http.Error(w, "", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	c := p.c
	c.mu.Lock()
	var oa *openArchive
	for _, cand := range c.archives {
		if cand.token != "" && subtle.ConstantTimeCompare([]byte(cand.token), []byte(token)) == 1 {
			oa = cand
			break
		}
	}
	// The token is not enough: an archive whose page was left is draining
	// (APP.md §2.3, §4) — the bodies in flight finish, and it admits no new
	// request — and one the kill switch is closing has lost its token
	// already. mounted is the one predicate for both.
	if oa == nil || !oa.mounted || oa.quiesced || oa.state == "compacting" || oa.state == "needs_reopen" {
		c.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	info, found := oa.currentFile(fid)
	if !found {
		c.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	oa.readers++
	c.mu.Unlock()
	// A reader holds the archive open even after its page was left, and the
	// last one to end closes it (APP.md §2.3, §4).
	defer c.releaseReader(oa)
	rd, err := oa.a.OpenReader(fid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rd.Close()
	ct := mime.TypeByExtension(strings.ToLower(path.Ext(info.Name)))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline")
	// Whatever the type says, a preview is never a document that runs:
	// an HTML or SVG file opened as a top-level navigation is sandboxed.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, "", time.Unix(info.ModifiedAt, 0), rd)
}

// secretLife is how long a minted secret lives: the reveal's dialog, with
// its save after the digits were shown, never needs the ceremony again.
var secretLife = 10 * time.Minute

// mintSecret registers a one-time secret and returns its URL. The value is
// delivered once, to the app's origin; it stays behind the URL's token —
// the reveal's handle — until dropped or expired.
func (p *previewServer) mintSecret(value, label, id string) string {
	tok := newToken()
	p.mu.Lock()
	p.oneTime[tok] = oneTimeSecret{value: value, label: label, id: id, expires: p.c.now().Add(secretLife)}
	p.mu.Unlock()
	return fmt.Sprintf("http://127.0.0.1:%d/s/%s", p.port, tok)
}

// secretValue is the value behind a handle, fetched or not, while it
// lives, with the label of the way in it belongs to.
func (p *previewServer) secretValue(tok string) (value, label, id string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.oneTime[tok]
	if !ok || p.c.now().After(s.expires) {
		delete(p.oneTime, tok)
		return "", "", "", false
	}
	return s.value, s.label, s.id, true
}

// dropSecret ends a handle: the dialog closed.
func (p *previewServer) dropSecret(tok string) {
	p.mu.Lock()
	delete(p.oneTime, tok)
	p.mu.Unlock()
}

// dropAllSecrets ends every handle: a lock trigger fired.
func (p *previewServer) dropAllSecrets() {
	p.mu.Lock()
	clear(p.oneTime)
	p.mu.Unlock()
}

// serveSecret: GET /s/<token>, once.
func (p *previewServer) serveSecret(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	p.mu.Lock()
	origin := p.appOrigin
	p.mu.Unlock()
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	if r.Method == http.MethodOptions {
		// A preflight must not consume the secret.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	tok := strings.TrimPrefix(r.URL.Path, "/s/")
	p.mu.Lock()
	s, ok := p.oneTime[tok]
	good := ok && !s.fetched && !p.c.now().After(s.expires) && (origin == "" || r.Header.Get("Origin") == origin)
	if good {
		// The URL is consumed; the value stays behind the handle.
		s.fetched = true
		p.oneTime[tok] = s
	} else {
		// A second, a foreign or a late fetch ends the handle too.
		delete(p.oneTime, tok)
	}
	for k, v := range p.oneTime {
		if p.c.now().After(v.expires) {
			delete(p.oneTime, k)
		}
	}
	p.mu.Unlock()
	if !good {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(s.value))
}

var errPreviewDown = errors.New("preview server not running")
