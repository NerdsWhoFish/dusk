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

	w.Header().Set("Cache-Control", assetMaxAge)
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
