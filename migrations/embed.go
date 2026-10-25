// Package migrations встраивает SQL-миграции в бинарник API.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
