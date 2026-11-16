package yamlutil

import (
	"errors"
	"testing"
)

func TestSafeLineLocationDoesNotReturnParserValues(t *testing.T) {
	cases := []struct {
		message, want string
	}{
		{"yaml: line 42: bad private-secret", " at line 42"},
		{"line 3: field secretValue not found in type private", " at line 3"},
		{"invalid YAML secret text", ""},
		{"secret key line 0000000000000009999 message", ""},
	}
	for _, tc := range cases {
		got := SafeLineLocation(errors.New(tc.message))
		if got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.message, got, tc.want)
		}
	}
	if SafeLineLocation(nil) != "" {
		t.Fatal("nil parser error produced location")
	}
}
