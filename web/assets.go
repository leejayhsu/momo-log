package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// StaticFiles contains the web package's self-hosted assets.
//
//go:embed static/*
var StaticFiles embed.FS

// StaticHandler serves embedded assets at paths relative to /static/.
func StaticHandler() http.Handler {
	static, err := fs.Sub(StaticFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(static))
}
