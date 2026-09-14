package dashboard

import (
	"embed"
	"net/http"
)

//go:embed index.html app.js analytics.js style.css vendor/*.js vendor/*.txt
var assets embed.FS

var assetPaths = map[string]string{
	"/dashboard": "index.html", "/dashboard/": "index.html",
	"/dashboard/app.js": "app.js", "/dashboard/analytics.js": "analytics.js", "/dashboard/style.css": "style.css",
	"/dashboard/vendor/echarts-6.1.0.min.js":   "vendor/echarts-6.1.0.min.js",
	"/dashboard/vendor/ECHARTS-LICENSE.txt":    "vendor/ECHARTS-LICENSE.txt",
	"/dashboard/vendor/ECHARTS-NOTICE.txt":     "vendor/ECHARTS-NOTICE.txt",
	"/dashboard/vendor/ECHARTS-D3-LICENSE.txt": "vendor/ECHARTS-D3-LICENSE.txt",
}

// IsAssetPath distinguishes the public login shell and its assets from the
// authenticated metrics API. Never use a prefix match for this boundary.
func IsAssetPath(path string) bool {
	_, ok := assetPaths[path]
	return ok
}

// Static serves only explicitly listed embedded assets. Metrics authentication belongs
// to the proxy; no secret or usage data is embedded in the public login shell.
func Static(w http.ResponseWriter, r *http.Request) {
	name, ok := assetPaths[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	contentType := "text/plain; charset=utf-8"
	switch name {
	case "index.html":
		contentType = "text/html; charset=utf-8"
	case "app.js", "analytics.js", "vendor/echarts-6.1.0.min.js":
		contentType = "text/javascript; charset=utf-8"
	case "style.css":
		contentType = "text/css; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	data, _ := assets.ReadFile(name)
	_, _ = w.Write(data)
}
