package names

import (
	"strings"
	"testing"
)

func TestIsIdentifierAcceptsDottedASCIIIdentifiers(t *testing.T) {
	for _, name := range []string{"_", "_private", "A", "a1", "api.v2.get", "A.B2", "billing.invoice_get"} {
		if !IsIdentifier(name) {
			t.Errorf("IsIdentifier(%q) = false, want true", name)
		}
	}
}

func TestIsIdentifierRejectsMalformedOrNonASCIINames(t *testing.T) {
	for _, name := range []string{"", ".", "a.", ".a", "a..b", "1a", "a-b", "a/b", "a b", "café", "é"} {
		if IsIdentifier(name) {
			t.Errorf("IsIdentifier(%q) = true, want false", name)
		}
	}
}

func TestValidateIdentifiersAcceptsEmptyListsAndReportsFirstInvalidName(t *testing.T) {
	for _, names := range [][]string{nil, {}, {"api.get", "_private", "item2"}} {
		if err := ValidateIdentifiers(names); err != nil {
			t.Errorf("ValidateIdentifiers(%v) = %v, want nil", names, err)
		}
	}

	err := ValidateIdentifiers([]string{"valid_name", "bad-name", "later"})
	if err == nil || err.Error() != `method name "bad-name" must be a valid identifier` {
		t.Fatalf("ValidateIdentifiers error = %v, want first invalid name", err)
	}
}

func TestSnakeCaseHandlesAcronymsAndMixedSeparators(t *testing.T) {
	cases := map[string]string{
		"HTTPServerID":             "http_server_id",
		"XMLHttpRequest":           "xml_http_request",
		"version2API":              "version2_api",
		"user.ID":                  "user_id",
		"__Already--Spaced..Name ": "already_spaced_name",
		"caféName":                 "café_name",
		"":                         "",
	}
	for input, want := range cases {
		if got := SnakeCase(input); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSnakeCaseProducesOnlyLowercaseWordSeparatorsForASCIIInput(t *testing.T) {
	for _, input := range []string{"HTTPServerID", "user.get", "already-snake case", "v2Status"} {
		got := SnakeCase(input)
		if got != strings.ToLower(got) || strings.ContainsAny(got, ".- ") {
			t.Errorf("SnakeCase(%q) = %q, want lowercase underscore-separated output", input, got)
		}
	}
}
