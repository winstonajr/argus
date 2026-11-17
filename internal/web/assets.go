package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

type cachedAsset struct {
	raw         []byte
	gzipped     []byte
	contentType string
	etag        string
	hash        string
}

func (s *Server) initStaticAssets() error {
	s.assets = make(map[string]cachedAsset)

	return fs.WalkDir(staticFS, "static", func(filePath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := staticFS.ReadFile(filePath)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(filePath, "static/")
		sum := sha256.Sum256(data)
		shortHash := hex.EncodeToString(sum[:6])
		etag := `"` + shortHash + `"`

		ct := mime.TypeByExtension(filepath.Ext(filePath))
		if ct == "" {
			ct = http.DetectContentType(data)
		}

		var gzData []byte
		if shouldCompress(ct, len(data)) {
			var buf bytes.Buffer
			gz, gzErr := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			if gzErr == nil {
				_, _ = gz.Write(data)
				if gz.Close() == nil && buf.Len() < len(data) {
					gzData = buf.Bytes()
				}
			}
		}

		s.assets[rel] = cachedAsset{
			raw:         data,
			gzipped:     gzData,
			contentType: ct,
			etag:        etag,
			hash:        shortHash,
		}
		return nil
	})
}

func shouldCompress(contentType string, size int) bool {
	if size < 256 {
		return false
	}
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "svg") ||
		strings.Contains(ct, "xml")
}

func (s *Server) assetURL(relPath string) string {
	clean := strings.TrimPrefix(path.Clean(relPath), "/")
	if a, ok := s.assets[clean]; ok && a.hash != "" {
		return "/static/" + clean + "?v=" + a.hash
	}
	return "/static/" + clean
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/static/")
	a, ok := s.assets[rel]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("ETag", a.etag)
	w.Header().Set("Vary", "Accept-Encoding")
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}

	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if len(a.gzipped) > 0 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(a.gzipped)
		return
	}

	_, _ = w.Write(a.raw)
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	a, ok := s.assets["favicon.ico"]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("ETag", a.etag)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(a.raw)
}
