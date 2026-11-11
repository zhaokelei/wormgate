package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// gzipMinSize is the payload size below which compression costs more than it
// saves (index.html is ~2 KB and already benefits; tiny API replies do not).
const gzipMinSize = 1024

// compressible reports whether a Vite asset benefits from gzip. Fonts, images
// and the source maps (not shipped) are already compressed or skipped.
func compressible(name string) bool {
	switch {
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".css"),
		strings.HasSuffix(name, ".html"), strings.HasSuffix(name, ".json"),
		strings.HasSuffix(name, ".svg"), strings.HasSuffix(name, ".txt"),
		strings.HasSuffix(name, ".map"):
		return true
	default:
		return false
	}
}

// gzipPool reuses writers: a console page issues ~10 asset requests, and each
// gzip.NewWriter allocation is ~50 KB of window state.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// Compress compresses text responses when the client accepts gzip.
//
// Wrap only static console handlers with it, not the API mux: JSON replies are
// small, and SSE streams must not be buffered by a compressor.
//
// SECURITY: only text-ish assets are compressed, and Vary is always set so a
// shared cache cannot hand a gzip body to a client that did not ask for one.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || !compressible(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	zw          *gzip.Writer
	status      int
	decided     bool
	compressing bool
}

// WriteHeader defers the decision: it is only safe to switch to gzip before the
// first body byte, and Content-Length must be dropped when we do.
func (g *gzipResponseWriter) WriteHeader(status int) {
	g.status = status
	if g.decided {
		g.ResponseWriter.WriteHeader(status)
	}
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.decided {
		g.decide(len(p))
	}
	if g.compressing {
		return g.zw.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

func (g *gzipResponseWriter) decide(size int) {
	g.decided = true
	h := g.ResponseWriter.Header()
	status := g.status
	if status == 0 {
		status = http.StatusOK
	}
	g.compressing = size >= gzipMinSize && status == http.StatusOK
	if g.compressing {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Set("X-Content-Type-Options", "nosniff")
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(g.ResponseWriter)
		g.zw = zw
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Close() {
	if g.zw != nil {
		_ = g.zw.Close()
		gzipPool.Put(g.zw)
		g.zw = nil
	}
}

// Flush forwards to the underlying writer so SSE streams keep working when the
// middleware is wrapped around a handler that flushes (the log view).
func (g *gzipResponseWriter) Flush() {
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// SPAHandler serves a Vite build from fsys and falls back to index.html for
// client-side routes. prefix is "" for the root console or "/admin" for the
// server console.
func SPAHandler(fsys fs.FS, prefix string) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if prefix != "" && prefix != "/" {
			if p == prefix {
				http.Redirect(w, r, prefix+"/", http.StatusTemporaryRedirect)
				return
			}
			if !strings.HasPrefix(p, prefix+"/") {
				http.NotFound(w, r)
				return
			}
			p = strings.TrimPrefix(p, prefix)
			if p == "" {
				p = "/"
			}
		}
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if name != "" {
			if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
				// Vite emits content-hashed filenames under /assets, so they can
				// be cached for a year; index.html must never be cached or a
				// deploy would not reach an open tab.
				if strings.HasPrefix(p, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				rr := r.Clone(r.Context())
				u := *r.URL
				u.Path = "/" + name
				rr.URL = &u
				files.ServeHTTP(w, rr)
				return
			}
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.Error(w, "console assets are missing; run `make web`", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}

// SecurityHeaders applies conservative browser hardening to console and API
// responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self' data:; "+
				"connect-src 'self' ws: wss:; "+
				"frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
