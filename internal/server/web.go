package server

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/FetchHQ/dusk/web"
)

// assetMaxAge is long because the binary is the versioned artifact: a given
// build's assets never change, and index.html naming them is never cached.
const assetMaxAge = "public, max-age=31536000, immutable"

// staticMaxAge is for files served under a fixed name, which a new build
// replaces in place. Immutable would pin every browser that ever loaded one to
// the first version it saw, and a favicon is not worth a permanent mistake.
const staticMaxAge = "public, max-age=3600"

// hashedAssets is where Vite writes content-hashed output. Anything outside it
// came from public/ and keeps the name it was written with.
const hashedAssets = "assets/"

// icons are the paths handleIcon will serve. Everything else about the UI is
// gated; these are brand assets that identify Dusk to a tab strip.
var icons = []string{"/favicon.svg", "/apple-touch-icon.png"}

// handleIcon serves an icon from outside the gate, for the same reason signing
// in sits outside it: the login and setup pages reference these, so a gated
// icon redirects to the very page asking for it and renders nothing.
func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	root, ok := web.Bundle()
	if !ok {
		http.NotFound(w, r)
		return
	}

	body, err := fs.ReadFile(root, strings.TrimPrefix(r.URL.Path, "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", staticMaxAge)
	if strings.HasSuffix(r.URL.Path, ".svg") {
		w.Header().Set("Content-Type", "image/svg+xml")
	} else {
		w.Header().Set("Content-Type", "image/png")
	}
	if _, err := w.Write(body); err != nil {
		s.log.Debug("the browser went away mid-icon", "error", err)
	}
}

// handleApp serves the single page app. Every unmatched path returns
// index.html so a deep link to an entity survives a refresh, which is the one
// thing a client-routed app gets wrong if the server is not told.
func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	root, ok := web.Bundle()
	if !ok {
		http.Error(w, "this build has no UI: run `make web` and rebuild", http.StatusNotImplemented)
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		s.serveIndex(w, r, root)
		return
	}

	file, err := root.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		s.serveIndex(w, r, root)
		return
	}
	if err != nil {
		http.Error(w, "could not read the UI", http.StatusInternalServerError)
		return
	}
	_ = file.Close()

	if strings.HasPrefix(name, hashedAssets) {
		w.Header().Set("Cache-Control", assetMaxAge)
	} else {
		w.Header().Set("Cache-Control", staticMaxAge)
	}
	http.FileServerFS(root).ServeHTTP(w, r)
}

// serveIndex writes the shell. It is never cached, because it is the only
// thing naming the current asset bundle and a stale copy pins a browser to a
// version that no longer exists.
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	page, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.Error(w, "could not read the UI", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(page); err != nil {
		s.log.Debug("the browser went away mid-page", "error", err)
	}
}
