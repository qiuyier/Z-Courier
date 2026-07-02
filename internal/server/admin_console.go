package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	adminConsoleCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

	adminConsoleIndexCacheControl = "no-store"
	adminConsoleAssetCacheControl = "public, max-age=31536000, immutable"
	adminConsoleOtherCacheControl = "no-cache"
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
	setAdminConsoleSecurityHeaders(w)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Cache-Control", adminConsoleIndexCacheControl)
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
		w.Header().Set("Cache-Control", adminConsoleIndexCacheControl)
		http.NotFound(w, r)
		return
	}

	targetPath := filepath.Join(h.assetsDir, cleanPath)
	info, err := os.Stat(targetPath)
	if err == nil && !info.IsDir() {
		setAdminConsoleCacheHeader(w, cleanPath)
		http.ServeFile(w, r, targetPath)
		return
	}

	if strings.Contains(filepath.Base(cleanPath), ".") {
		w.Header().Set("Cache-Control", adminConsoleIndexCacheControl)
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

func (h *adminConsoleHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	indexPath := filepath.Join(h.assetsDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		w.Header().Set("Cache-Control", adminConsoleIndexCacheControl)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", adminConsoleIndexCacheControl)
	http.ServeFile(w, r, indexPath)
}

func setAdminConsoleSecurityHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Content-Security-Policy", adminConsoleCSP)
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func setAdminConsoleCacheHeader(w http.ResponseWriter, cleanPath string) {
	if strings.HasPrefix(filepath.ToSlash(cleanPath), "assets/") {
		w.Header().Set("Cache-Control", adminConsoleAssetCacheControl)
		return
	}
	w.Header().Set("Cache-Control", adminConsoleOtherCacheControl)
}
