// Package web встраивает статический веб-интерфейс в бинарник API.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS — файлы интерфейса (корень = каталог static).
func FS() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
