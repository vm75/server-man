package declarative

import (
	"errors"
	"strconv"
	"strings"
)

// unitValue quotes a word for tokenized systemd/Quadlet settings and escapes specifier expansion.
func unitValue(s string) string { return strconv.Quote(strings.ReplaceAll(s, "%", "%%")) }

// Scalar directives retain quote characters. Unlike tokenized Environment/Exec
// values, they need literal specifier escaping rather than word quoting.
func unitScalarValue(s string) (string, error) {
	if !literal(s) || strings.TrimSpace(s) != s || strings.Contains(s, "\\") {
		return "", errors.New("unit scalar contains unsupported whitespace or path escapes")
	}
	return strings.ReplaceAll(s, "%", "%%"), nil
}
