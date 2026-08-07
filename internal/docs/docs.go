// Package docs provides Swagger UI at /docs and the OpenAPI 3.0 spec at
// /swagger.json and /openapi.json.
package docs

import (
	_ "embed"
	"net/http"
)

//go:embed swagger.json
var swaggerSpec []byte

// swaggerUIHTML is the Swagger UI page loading from CDN.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Scratchpad Engine API</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.10.3/swagger-ui.css">
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.3/swagger-ui-bundle.js"></script>
    <script>
        SwaggerUIBundle({
            url: '/swagger.json',
            dom_id: '#swagger-ui',
            presets: [
                SwaggerUIBundle.presets.apis,
                SwaggerUIBundle.presets.standalone
            ]
        });
    </script>
</body>
</html>`

// Handler routes the OpenAPI spec JSON for /swagger.json and /openapi.json,
// and the Swagger UI for every other path (/docs and anything else, so a bare
// index visit still lands on the documentation page).
func Handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/swagger.json", "/openapi.json":
		swaggerJSON(w, r)
	default:
		swaggerUI(w, r)
	}
}

func swaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(swaggerUIHTML))
}

func swaggerJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(swaggerSpec)
}
