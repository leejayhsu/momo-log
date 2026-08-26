package web

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

// StaticFiles contains the web package's self-hosted assets.
//
//go:embed static/*
var StaticFiles embed.FS

var staticHashes sync.Map

func staticURL(name string) string {
	hash, err := staticHash(name)
	if err != nil {
		panic(err)
	}
	return "/static/" + hash + "/" + name
}

func staticHash(name string) (string, error) {
	if hash, ok := staticHashes.Load(name); ok {
		return hash.(string), nil
	}
	data, err := StaticFiles.ReadFile("static/" + name)
	if err != nil {
		return "", fmt.Errorf("hash static asset %q: %w", name, err)
	}
	sum := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", sum[:6])
	staticHashes.Store(name, hash)
	return hash, nil
}

// StaticHandler serves embedded assets at paths relative to /static/.
func StaticHandler() http.Handler {
	static, err := fs.Sub(StaticFiles, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		hash, name, hashed := strings.Cut(path, "/")
		if hashed && len(hash) == 12 {
			expected, err := staticHash(name)
			if err != nil || hash != expected {
				http.NotFound(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/" + name
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
