package push

import "testing"

func TestNewNormalizesMailtoSubjectForWebpushGo(t *testing.T) {
	service := New(nil, "public", "private", "mailto:momo@example.com")
	if service.subscriber != "momo@example.com" {
		t.Fatalf("subscriber = %q; want %q", service.subscriber, "momo@example.com")
	}
}

func TestNewPreservesHTTPSSubject(t *testing.T) {
	service := New(nil, "public", "private", "https://example.com/contact")
	if service.subscriber != "https://example.com/contact" {
		t.Fatalf("subscriber = %q; want %q", service.subscriber, "https://example.com/contact")
	}
}
