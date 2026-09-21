package names

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latch/protocol"
)

// AssignTypeNames deterministically assigns each named type a display name
// suitable for a flat generated-language namespace. Types are keyed by their
// bare Go type name when unique; on collision (the same GoName from two
// different Go packages) each is prefixed by its package's last path
// segment. If that still collides, generation fails with a descriptive
// error rather than silently renaming anything further.
func AssignTypeNames(types []*protocol.NamedType) (map[string]string, error) {
	byName := map[string][]*protocol.NamedType{}
	for _, t := range types {
		byName[t.GoName] = append(byName[t.GoName], t)
	}

	result := map[string]string{}
	for goName, group := range byName {
		if len(group) == 1 {
			result[group[0].ID] = goName
			continue
		}

		used := map[string]string{} // candidate name -> ID that claimed it
		for _, t := range group {
			candidate := PascalCase(lastPathSegment(t.GoPkgPath)) + goName
			if owner, dup := used[candidate]; dup {
				return nil, fmt.Errorf(
					"latchwire: type name collision: %q and %q both generate the name %q; rename one of the Go types",
					owner, t.ID, candidate,
				)
			}
			used[candidate] = t.ID
			result[t.ID] = candidate
		}
	}
	return result, nil
}

func lastPathSegment(pkgPath string) string {
	idx := strings.LastIndex(pkgPath, "/")
	if idx == -1 {
		return pkgPath
	}
	return pkgPath[idx+1:]
}

// MethodNode is one node of the nested namespace tree built from dotted
// method names, e.g. "billing.invoice.get" contributes the path
// billing -> invoice -> get. Every generated-client namespace object
// (client.billing.invoice.get(...)) is built by walking this tree.
type MethodNode struct {
	Segment    string
	FullName   string // set only on a leaf (an actual registered method)
	IsLeaf     bool
	Children   map[string]*MethodNode
	ChildOrder []string
}

func newMethodNode(segment string) *MethodNode {
	return &MethodNode{Segment: segment, Children: map[string]*MethodNode{}}
}

// BuildMethodTree builds the nested namespace tree for a set of registered
// method names, sorted deterministically, and fails if any method name
// collides with another method used as a namespace prefix (e.g. both
// "user" and "user.get" registered at once).
func BuildMethodTree(methodNames []string) (*MethodNode, error) {
	sorted := append([]string(nil), methodNames...)
	sort.Strings(sorted)

	root := newMethodNode("")
	for _, full := range sorted {
		segs := Segments(full)
		cur := root
		for i, seg := range segs {
			last := i == len(segs)-1
			child, ok := cur.Children[seg]
			if !ok {
				child = newMethodNode(seg)
				cur.Children[seg] = child
				cur.ChildOrder = append(cur.ChildOrder, seg)
			}
			if child.IsLeaf && !last {
				return nil, fmt.Errorf(
					"latchwire: method name %q cannot be used as a namespace because %q is already a registered method",
					full, child.FullName,
				)
			}
			if last {
				if len(child.Children) > 0 {
					return nil, fmt.Errorf(
						"latchwire: method name %q collides with a namespace already used by other methods (e.g. %q)",
						full, full+"."+child.ChildOrder[0],
					)
				}
				child.IsLeaf = true
				child.FullName = full
			}
			cur = child
		}
	}
	return root, nil
}
