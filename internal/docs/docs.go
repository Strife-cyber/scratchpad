// Package docs provides Swagger UI at /docs and OpenAPI spec at /swagger.json.
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

// Handler routes to appropriate handler based on path.
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/swagger.json" {
		swaggerJSON(w, r)
		return
	}
	swaggerUI(w, r)
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
