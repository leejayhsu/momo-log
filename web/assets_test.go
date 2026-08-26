package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticHandlerServesHashedAsset(t *testing.T) {
	path := staticURL("app.css")
	request := httptest.NewRequest("GET", path, nil)
	request.URL.Path = strings.TrimPrefix(path, "/static")
	recorder := httptest.NewRecorder()

	StaticHandler().ServeHTTP(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
