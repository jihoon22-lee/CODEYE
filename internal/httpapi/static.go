package httpapi

import (
	"net/http"
	"path"
	"strings"
)

// serveStatic serves the built web app from WebDir through os.Root, so paths
// and symlinks cannot escape the directory. Directory listings are never
// produced. Extension-less paths fall back to index.html (client routing);
// missing files with an extension are 404.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.web == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("CODEYE web assets are not built. The API is available under /api/v1.\n"))
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	name := strings.TrimPrefix(clean, "/")
	if name == "" || strings.HasSuffix(r.URL.Path, "/") {
		name = "index.html"
	}
	if s.serveFile(w, r, name) {
		return
	}
	if path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	if !s.serveFile(w, r, "index.html") {
		http.NotFound(w, r)
	}
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := s.web.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
	return true
}
