// Package api встраивает OpenAPI-спецификацию в бинарник (раздаётся по /api/v1/openapi.yaml).
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
