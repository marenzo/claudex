package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticAssetBoundaryAndSecurityHeaders(t *testing.T) {
	for path := range assetPaths {
		if !IsAssetPath(path) {
			t.Fatalf("asset %q missing from allowlist", path)
		}
		w := httptest.NewRecorder()
		Static(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("asset %q not embedded: %d", path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("asset %q missing privacy headers", path)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") || strings.Contains(csp, "unsafe-") {
			t.Fatalf("asset %q weak content policy: %s", path, csp)
		}
		if strings.HasSuffix(path, ".js") && w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Fatalf("JavaScript asset %q has wrong content type", path)
		}
	}
	for _, path := range []string{"/dashboard/api", "/dashboard/api/", "/dashboard/vendor/README.md", "/dashboard/metrics.go", "/dashboard/../config.json", "/dashboard/app.js/extra"} {
		if IsAssetPath(path) {
			t.Fatalf("non-asset %q bypasses authentication", path)
		}
		w := httptest.NewRecorder()
		Static(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("unexpected public file %q", path)
		}
	}
}
