package main

import "testing"

func TestValidVAPIDSubject(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "https://example.com/contact", want: true},
		{value: "mailto:momo@example.com", want: true},
		{value: "mailto:momo@localhost", want: false},
		{value: "http://example.com/contact", want: false},
		{value: "momo@example.com", want: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := validVAPIDSubject(test.value); got != test.want {
				t.Fatalf("validVAPIDSubject(%q) = %t; want %t", test.value, got, test.want)
			}
		})
	}
}
