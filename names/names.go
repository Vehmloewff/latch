// Package names provides deterministic identifier transformations shared by
// every code generator: splitting a dotted protocol name ("billing.invoice.get")
// into path segments, and converting between camelCase, PascalCase, and
// snake_case.
package names

import (
	"fmt"
	"strings"
)

// IsSnakeCase reports whether s is a non-empty ASCII snake_case name. Names
// must start with a lowercase letter, contain only lowercase letters, digits,
// and underscores, and may not contain leading, trailing, or repeated
// underscores.
func IsSnakeCase(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	previousUnderscore := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_':
			if previousUnderscore {
				return false
			}
			previousUnderscore = true
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			previousUnderscore = false
		default:
			return false
		}
	}
	return !previousUnderscore
}

// IsIdentifier reports whether s is a non-empty ASCII identifier.
//
// Deprecated: method names must use IsSnakeCase.
func IsIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// ValidateIdentifiers checks the method names in an IR before a generator
// emits them as source-level methods.
func ValidateIdentifiers(methodNames []string) error {
	for _, name := range methodNames {
		if !IsSnakeCase(name) {
			return fmt.Errorf("method name %q must be snake_case", name)
		}
	}
	return nil
}

// Segments splits a dotted method or event name into its path components.
// "user.get" -> ["user", "get"]. "math.add" -> ["math", "add"].
func Segments(dotted string) []string {
	return strings.Split(dotted, ".")
}

// splitWords breaks an identifier into lowercase words regardless of its
// input casing (camelCase, PascalCase, snake_case, or dotted segments).
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}

	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
		case r >= 'A' && r <= 'Z':
			// Start a new word on an upper-case letter unless it continues
			// an existing all-caps run (e.g. "ID" stays one word).
			if i > 0 {
				prev := runes[i-1]
				prevUpper := prev >= 'A' && prev <= 'Z'
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if !prevUpper || nextLower {
					flush()
				}
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return words
}

// PascalCase converts s to PascalCase: "user.get" -> "UserGet", "displayName" -> "DisplayName".
func PascalCase(s string) string {
	words := splitWords(s)
	var b strings.Builder
	for _, w := range words {
		b.WriteString(strings.ToUpper(w[:1]))
		if len(w) > 1 {
			b.WriteString(w[1:])
		}
	}
	return b.String()
}

// CamelCase converts s to camelCase: "user.get" -> "userGet", "display_name" -> "displayName".
func CamelCase(s string) string {
	p := PascalCase(s)
	if p == "" {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// SnakeCase converts s to snake_case: "DisplayName" -> "display_name".
func SnakeCase(s string) string {
	words := splitWords(s)
	return strings.Join(words, "_")
}
