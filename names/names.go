// Package names provides deterministic identifier transformations shared by
// every code generator: splitting a dotted protocol name ("billing.invoice.get")
// into path segments, and converting between camelCase, PascalCase, and
// snake_case.
package names

import "strings"

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
