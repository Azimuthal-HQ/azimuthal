// Package apidocs embeds the OpenAPI spec for inclusion in the binary.
package apidocs

import _ "embed"

// OpenAPISpec is the raw OpenAPI 3.0 YAML specification.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
