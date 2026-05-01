package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	apidocs "github.com/Azimuthal-HQ/azimuthal/docs/api"
)

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Azimuthal API Reference</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
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
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
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

	r.Get("/api/docs/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(apidocs.OpenAPISpec)
	})
}
