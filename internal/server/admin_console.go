package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type adminConsoleHandler struct {
	mountPath string
	assetsDir string
}

func newAdminConsoleHandler(config AdminConsoleConfig) http.Handler {
	return &adminConsoleHandler{
		mountPath: config.Path,
		assetsDir: config.AssetsDir,
	}
}

func (h *adminConsoleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", strings.Join([]string{http.MethodGet, http.MethodHead}, ", "))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	relativePath := strings.TrimPrefix(r.URL.Path, h.mountPath)
	if relativePath == "" || relativePath == "." {
		h.serveIndex(w, r)
		return
	}

	cleanPath := filepath.Clean(filepath.FromSlash(relativePath))
	if cleanPath == "." || strings.HasPrefix(cleanPath, "..") {
		http.NotFound(w, r)
		return
	}

	targetPath := filepath.Join(h.assetsDir, cleanPath)
	info, err := os.Stat(targetPath)
	if err == nil && !info.IsDir() {
		http.ServeFile(w, r, targetPath)
		return
	}

	if strings.Contains(filepath.Base(cleanPath), ".") {
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

func (h *adminConsoleHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(h.assetsDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, indexPath)
}
