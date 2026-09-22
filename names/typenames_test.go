package names

import (
	"strings"
	"testing"

	"github.com/vehmloewff/latch/protocol"
)

func TestAssignTypeNamesUsesBareNamesWhenUnique(t *testing.T) {
	types := []*protocol.NamedType{
		{ID: "example/invoice.Invoice", GoPkgPath: "example/invoice", GoName: "Invoice", Kind: protocol.KindStruct},
		{ID: "example/status.Status", GoPkgPath: "example/status", GoName: "Status", Kind: protocol.KindEnum},
	}
	got, err := AssignTypeNames(types)
	if err != nil {
		t.Fatalf("AssignTypeNames: %v", err)
	}
	want := map[string]string{
		"example/invoice.Invoice": "Invoice",
		"example/status.Status":   "Status",
	}
	if len(got) != len(want) {
		t.Fatalf("assigned names = %#v, want %#v", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("assigned name for %q = %q, want %q", id, got[id], name)
		}
	}
}

func TestAssignTypeNamesDisambiguatesPackageCollisions(t *testing.T) {
	types := []*protocol.NamedType{
		{ID: "example/billing/invoice.Record", GoPkgPath: "example/billing/invoice", GoName: "Record", Kind: protocol.KindStruct},
		{ID: "example/shipping/label.Record", GoPkgPath: "example/shipping/label", GoName: "Record", Kind: protocol.KindStruct},
	}
	got, err := AssignTypeNames(types)
	if err != nil {
		t.Fatalf("AssignTypeNames: %v", err)
	}
	if got[types[0].ID] != "InvoiceRecord" || got[types[1].ID] != "LabelRecord" {
		t.Fatalf("assigned names = %#v, want InvoiceRecord and LabelRecord", got)
	}
}

func TestAssignTypeNamesRejectsSamePackageSegmentCollision(t *testing.T) {
	types := []*protocol.NamedType{
		{ID: "example/one/models.Record", GoPkgPath: "example/one/models", GoName: "Record", Kind: protocol.KindStruct},
		{ID: "example/two/models.Record", GoPkgPath: "example/two/models", GoName: "Record", Kind: protocol.KindStruct},
	}
	_, err := AssignTypeNames(types)
	if err == nil {
		t.Fatal("AssignTypeNames succeeded despite a generated-name collision")
	}
	if !strings.Contains(err.Error(), "type name collision") || !strings.Contains(err.Error(), "ModelsRecord") {
		t.Fatalf("collision error = %q, want generated name and collision context", err)
	}
}

func TestBuildMethodTreeSortsChildrenAndStoresLeaves(t *testing.T) {
	root, err := BuildMethodTree([]string{
		"user.update",
		"billing.invoice.get",
		"user.get",
		"ping",
	})
	if err != nil {
		t.Fatalf("BuildMethodTree: %v", err)
	}
	if root.ChildOrder[0] != "billing" || root.ChildOrder[1] != "ping" || root.ChildOrder[2] != "user" {
		t.Fatalf("root child order = %v, want deterministic sorted order", root.ChildOrder)
	}
	if root.Children["ping"].FullName != "ping" || !root.Children["ping"].IsLeaf {
		t.Fatalf("ping node = %#v, want leaf for ping", root.Children["ping"])
	}
	user := root.Children["user"]
	if user.ChildOrder[0] != "get" || user.ChildOrder[1] != "update" {
		t.Fatalf("user child order = %v, want [get update]", user.ChildOrder)
	}
	if user.Children["get"].FullName != "user.get" || !user.Children["get"].IsLeaf {
		t.Fatalf("user.get node = %#v, want leaf", user.Children["get"])
	}
	invoice := root.Children["billing"].Children["invoice"]
	if invoice.Children["get"].FullName != "billing.invoice.get" {
		t.Fatalf("billing.invoice.get node = %#v", invoice.Children["get"])
	}
}

func TestBuildMethodTreeRejectsMethodNamespaceCollisions(t *testing.T) {
	cases := [][]string{
		{"user", "user.get"},
		{"user.get", "user"},
		{"billing.invoice", "billing.invoice.get"},
	}
	for _, methodNames := range cases {
		t.Run(strings.Join(methodNames, "_"), func(t *testing.T) {
			if _, err := BuildMethodTree(methodNames); err == nil {
				t.Fatalf("BuildMethodTree(%v) succeeded despite namespace collision", methodNames)
			} else if !strings.Contains(err.Error(), "collid") && !strings.Contains(err.Error(), "namespace") {
				t.Fatalf("collision error = %q", err)
			}
		})
	}
}
