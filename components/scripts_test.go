package components

import (
	"bytes"
	"testing"
)

func TestEmbeddedBundleIncludesInteractiveComponents(t *testing.T) {
	t.Setenv("GO_ENV", "production")
	js, _, _ := bundle()
	for _, component := range [][]byte{[]byte("window.tui.dialog"), []byte("window.tui.toast")} {
		if !bytes.Contains(js, component) {
			t.Fatalf("component bundle does not contain %q", component)
		}
	}
}
