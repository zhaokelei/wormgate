package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wormgate/wormgate/internal/webui"
	"github.com/wormgate/wormgate/internal/webui/adminnext"
)

// The grey-release console is mounted under /next and its Vite build is rooted
// at "/" inside the embed FS, so the handler has to strip the prefix before it
// can find a file. These cases pin that mapping down: a prefix regression would
// otherwise only show up as a blank page in production.
func TestSPAHandlerServesNextConsole(t *testing.T) {
	h := webui.SPAHandler(adminnext.FS(), "/next")

	t.Run("redirects bare prefix", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/next", nil))
		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("got %d, want %d", rec.Code, http.StatusTemporaryRedirect)
		}
		if loc := rec.Header().Get("Location"); loc != "/next/" {
			t.Fatalf("got Location %q, want /next/", loc)
		}
	})

	t.Run("serves index", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/next/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("got Content-Type %q, want text/html", ct)
		}
		body, _ := io.ReadAll(rec.Body)
		if !strings.Contains(string(body), "/next/assets/") {
			t.Fatalf("index.html must reference assets under /next/assets, got: %s", truncate(body))
		}
	})

	t.Run("falls back to index for client routes", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/next/users", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
			t.Fatal("expected the SPA shell")
		}
	})

	t.Run("does not shadow the api", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/next/api/users", nil))
		// The handler rewrites /next/api/... to /api/..., which it must refuse
		// rather than serve an index.html that would mask a bad API path.
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})

	t.Run("rejects paths outside the prefix", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})
}

func truncate(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "…"
	}
	return string(b)
}
