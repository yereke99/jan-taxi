// Package api embeds the OpenAPI contract so the binary can serve it at
// /api/v1/openapi.yaml and tests can check that every mounted route is documented.
package api

import _ "embed"

// Spec is the OpenAPI 3.1 document (openapi.yaml).
//
//go:embed openapi.yaml
var Spec []byte
