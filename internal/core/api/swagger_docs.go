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
    <script>
        window.onload = function() {
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
    </script>
</body>
</html>`

// RegisterDocsRoutes adds API documentation routes to the router.
// GET /api/docs           -> Swagger UI (interactive documentation)
// GET /api/docs/openapi.yaml -> raw OpenAPI 3.0 spec
func RegisterDocsRoutes(r chi.Router) {
	r.Get("/api/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(swaggerUIHTML))
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
