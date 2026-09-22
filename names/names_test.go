package names

import "testing"

func TestPascalCase(t *testing.T) {
	cases := map[string]string{
		"user.get":            "UserGet",
		"billing.invoice.get": "BillingInvoiceGet",
		"displayName":         "DisplayName",
		"user_id":             "UserId",
	}
	for in, want := range cases {
		if got := PascalCase(in); got != want {
			t.Errorf("PascalCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCamelCase(t *testing.T) {
	cases := map[string]string{
		"user.get":    "userGet",
		"DisplayName": "displayName",
		"project_id":  "projectId",
		"MemberIDs":   "memberIds",
		"URLValue":    "urlValue",
	}
	for in, want := range cases {
		if got := CamelCase(in); got != want {
			t.Errorf("CamelCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSnakeCase(t *testing.T) {
	valid := []string{"math_add", "room_subscribe", "v2_status", "a1"}
	for _, name := range valid {
		if !IsSnakeCase(name) {
			t.Errorf("IsSnakeCase(%q) = false, want true", name)
		}
	}

	invalid := []string{"", "MathAdd", "mathAdd", "math.add", "_math", "math_", "math__add", "math-add", "math add"}
	for _, name := range invalid {
		if IsSnakeCase(name) {
			t.Errorf("IsSnakeCase(%q) = true, want false", name)
		}
	}
}

func TestSegments(t *testing.T) {
	got := Segments("billing.invoice.get")
	want := []string{"billing", "invoice", "get"}
	if len(got) != len(want) {
		t.Fatalf("Segments() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Segments() = %v, want %v", got, want)
		}
	}
}
