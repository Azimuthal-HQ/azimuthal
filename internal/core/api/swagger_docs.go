package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	apidocs "github.com/Azimuthal-HQ/azimuthal/docs/api"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/swaggerui"
)

// The Swagger UI page loads its stylesheet and script bundles from this
// server, never from a CDN — see the swaggerui package for why. The assets are
// embedded in the binary, so /api/docs works on an isolated network and cannot
// be changed under an administrator by a third party.
//
// The page's initialiser is served as its own file rather than written inline,
// and that is a CSP requirement rather than a style choice. The global policy
// (api.ContentSecurityPolicy) is `script-src 'self'` with no 'unsafe-inline',
// no nonce and no hash, because keeping it bare is what makes it worth having.
// An inline <script> here would have forced one of those three concessions on
// every response in the product — including the wiki pages the policy exists
// for — to keep one internal documentation page working. So the script moved
// out instead. The <style> block below stays inline: style-src already carries
// 'unsafe-inline' for reasons the SPA imposes and this page cannot change.

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Azimuthal API Reference</title>
    <link rel="stylesheet" href="/api/docs/assets/swagger-ui.css">
    <style>
        * { box-sizing: border-box; }
        body { margin: 0; padding: 0; background: #0F1117; }
        .swagger-ui { background: #0F1117; }
        .swagger-ui .topbar {
            background: #1A1D27;
            border-bottom: 1px solid #2A2D3A;
            padding: 8px 16px;
        }
        .swagger-ui .topbar .download-url-wrapper { display: none; }
        .swagger-ui .topbar-wrapper img { display: none; }
        .swagger-ui .topbar-wrapper::before {
            content: 'Azimuthal API Reference';
            color: #4A90D9;
            font-size: 1.2rem;
            font-weight: 600;
            font-family: Inter, sans-serif;
        }
        .swagger-ui .info .title { color: #4A90D9; }
        .swagger-ui .info { background: #1A1D27; border-radius: 8px; padding: 16px; }
        .swagger-ui .scheme-container { background: #1A1D27; border-bottom: 1px solid #2A2D3A; }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="/api/docs/assets/swagger-ui-bundle.js"></script>
    <script src="/api/docs/assets/swagger-ui-standalone-preset.js"></script>
    <script src="/api/docs/init.js"></script>
</body>
</html>`

// swaggerUIInitJS is the page's initialiser, served from /api/docs/init.js.
// It was the inline <script> at the bottom of swaggerUIHTML until the global
// CSP arrived; see the comment above swaggerUIHTML for why it is a file now.
const swaggerUIInitJS = `window.onload = function() {
    SwaggerUIBundle({
        url: '/api/docs/openapi.yaml',
        dom_id: '#swagger-ui',
        presets: [
            SwaggerUIBundle.presets.apis,
            SwaggerUIStandalonePreset
        ],
        plugins: [SwaggerUIBundle.plugins.DownloadUrl],
        layout: 'StandaloneLayout',
        deepLinking: true,
        displayRequestDuration: true,
        defaultModelsExpandDepth: 2,
        defaultModelExpandDepth: 2,
        persistAuthorization: true,
        tryItOutEnabled: true,
        filter: true,
        syntaxHighlight: {
            activated: true,
            theme: 'monokai'
        }
    })
}
`

// RegisterDocsRoutes adds API documentation routes to the router.
// GET /api/docs              -> Swagger UI (interactive documentation)
// GET /api/docs/init.js      -> the UI page's initialiser (see swaggerUIHTML)
// GET /api/docs/openapi.yaml -> raw OpenAPI 3.0 spec
func RegisterDocsRoutes(r chi.Router) {
	r.Get("/api/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(swaggerUIHTML))
	})

	// Not under /api/docs/assets/: that path is the vendored Swagger UI tree
	// served straight out of an embedded FS, and it is cached `immutable` for a
	// year on the grounds that the path changes when the vendored version does.
	// This file is ours and changes when we change it, so it gets its own route
	// and no such caching.
	r.Get("/api/docs/init.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(swaggerUIInitJS))
	})

	// No Access-Control-Allow-Origin here. It used to be "*", which let any
	// page on the internet read this deployment's full API surface — every
	// route, parameter and schema — from a visiting administrator's browser.
	// The only consumer that needs it is the UI above, which is same-origin;
	// a genuine cross-origin consumer belongs in AZIMUTHAL_ALLOWED_ORIGINS,
	// where the router's CORS middleware will honour it like every other
	// route. This handler no longer decides CORS on its own.
	r.Get("/api/docs/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(apidocs.OpenAPISpec)
	})

	// Long-lived caching is safe: the assets are immutable for the life of the
	// binary, and the path changes when the vendored version does.
	// GET only. r.Handle would have registered every method, including PUT and
	// DELETE, on a static file tree — and the route sweep would have made us
	// classify nine routes where one belongs.
	r.Get("/api/docs/assets/*", http.StripPrefix("/api/docs/assets/",
		cacheForever(swaggerui.Handler())).ServeHTTP)
}

// cacheForever marks a response as immutable. Used only for the embedded
// Swagger UI assets.
func cacheForever(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}
