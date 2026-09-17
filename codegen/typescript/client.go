package typescript

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latchwire/names"
	"github.com/vehmloewff/latchwire/protocol"
)

// methodIndex maps a full dotted method name to its IR definition.
func methodIndex(p *protocol.Protocol) map[string]protocol.Method {
	idx := make(map[string]protocol.Method, len(p.Methods))
	for _, m := range p.Methods {
		idx[m.Name] = m
	}
	return idx
}

// renderMethodNamespace renders a non-leaf namespace node as a TypeScript
// object literal, e.g. { invoice: { get: (req) => this.call(...) } }.
func renderMethodNamespace(node *names.MethodNode, methods map[string]protocol.Method, typeNames map[string]string, indent string) string {
	var b strings.Builder
	b.WriteString("{\n")
	inner := indent + "  "
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		b.WriteString(inner)
		b.WriteString(names.CamelCase(seg))
		b.WriteString(": ")
		if child.IsLeaf {
			m := methods[child.FullName]
			b.WriteString(fmt.Sprintf(
				"(req: %s): Promise<%s> => this.call(%q, req)",
				tsType(m.RequestType, typeNames), tsType(m.ResponseType, typeNames), child.FullName,
			))
		} else {
			b.WriteString(renderMethodNamespace(child, methods, typeNames, inner))
		}
		b.WriteString(",\n")
	}
	b.WriteString(indent + "}")
	return b.String()
}

// renderClientFields renders every top-level method/namespace as a class
// field declaration on the generated Connected*Client.
func renderClientFields(root *names.MethodNode, methods map[string]protocol.Method, typeNames map[string]string) string {
	var b strings.Builder
	for _, seg := range root.ChildOrder {
		child := root.Children[seg]
		b.WriteString("  readonly ")
		b.WriteString(names.CamelCase(seg))
		b.WriteString(" = ")
		if child.IsLeaf {
			m := methods[child.FullName]
			b.WriteString(fmt.Sprintf(
				"(req: %s): Promise<%s> => this.call(%q, req)",
				tsType(m.RequestType, typeNames), tsType(m.ResponseType, typeNames), child.FullName,
			))
		} else {
			b.WriteString(renderMethodNamespace(child, methods, typeNames, "  "))
		}
		b.WriteString(";\n")
	}
	return b.String()
}

// eventPropertyNames maps every event's full dotted name to its camelCase
// property name on the generated "events" object, failing if two events
// collide once camelCased (e.g. "user.updated" and "userUpdated").
func eventPropertyNames(p *protocol.Protocol) (map[string]string, error) {
	out := make(map[string]string, len(p.Events))
	used := make(map[string]string, len(p.Events))
	for _, e := range p.Events {
		prop := names.CamelCase(e.Name)
		if owner, dup := used[prop]; dup {
			return nil, fmt.Errorf(
				"typescript: events %q and %q both generate the property name %q; rename one of them",
				owner, e.Name, prop,
			)
		}
		used[prop] = e.Name
		out[e.Name] = prop
	}
	return out, nil
}

func renderEventsField(p *protocol.Protocol, eventProps map[string]string, typeNames map[string]string) string {
	var b strings.Builder
	b.WriteString("  readonly events = {\n")
	for _, e := range p.Events {
		b.WriteString(fmt.Sprintf("    %s: new EventStream<%s>(),\n", eventProps[e.Name], tsType(e.PayloadType, typeNames)))
	}
	b.WriteString("  };\n")
	return b.String()
}

func renderDispatchEvent(p *protocol.Protocol, eventProps map[string]string, typeNames map[string]string) string {
	var b strings.Builder
	b.WriteString("  protected dispatchEvent(env: { event?: string; payload?: unknown }): void {\n")
	b.WriteString("    switch (env.event) {\n")
	for _, e := range p.Events {
		b.WriteString(fmt.Sprintf(
			"      case %q:\n        this.events.%s._emit(env.payload as %s);\n        break;\n",
			e.Name, eventProps[e.Name], tsType(e.PayloadType, typeNames),
		))
	}
	b.WriteString("      default:\n        break;\n")
	b.WriteString("    }\n  }\n")
	return b.String()
}

// generateClientFile renders client.ts: the top-level "<Name>Client" (with
// a typed connect()) and "Connected<Name>Client" (the typed RPC/event
// surface) classes.
func generateClientFile(p *protocol.Protocol, clientName string, typeNames map[string]string) (string, error) {
	methodTree, err := names.BuildMethodTree(methodNames(p))
	if err != nil {
		return "", err
	}
	eventProps, err := eventPropertyNames(p)
	if err != nil {
		return "", err
	}

	methods := methodIndex(p)
	connectedName := "Connected" + clientName
	connectType := tsType(p.ConnectType, typeNames)

	var b strings.Builder

	typeImports := usedTypeNames(p, typeNames)
	if len(typeImports) > 0 {
		b.WriteString(fmt.Sprintf("import type { %s } from \"./types\";\n", strings.Join(typeImports, ", ")))
	}
	b.WriteString("import { BaseConnection, type ClientOptions, EventStream, connectSocket } from \"./runtime\";\n\n")

	fmt.Fprintf(&b, "export class %s {\n", clientName)
	b.WriteString("  private options: ClientOptions;\n\n")
	b.WriteString("  constructor(options: ClientOptions) {\n    this.options = options;\n  }\n\n")
	fmt.Fprintf(&b, "  async connect(params: %s): Promise<%s> {\n", connectType, connectedName)
	fmt.Fprintf(&b, "    const handshake = await connectSocket(this.options.url, this.options.webSocketFactory, %q, %q, params);\n", p.Name, p.Version)
	fmt.Fprintf(&b, "    return new %s(handshake);\n", connectedName)
	b.WriteString("  }\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "export class %s extends BaseConnection {\n", connectedName)
	b.WriteString(renderClientFields(methodTree, methods, typeNames))
	b.WriteString("\n")
	b.WriteString(renderEventsField(p, eventProps, typeNames))
	b.WriteString("\n")
	b.WriteString(renderDispatchEvent(p, eventProps, typeNames))
	b.WriteString("}\n")

	return b.String(), nil
}

func methodNames(p *protocol.Protocol) []string {
	out := make([]string, len(p.Methods))
	for i, m := range p.Methods {
		out[i] = m.Name
	}
	return out
}

// usedTypeNames collects the display names of every named type referenced
// directly by the connect type, a method request/response, or an event
// payload, sorted for deterministic import ordering. Types only referenced
// transitively (as a field of another named type) don't need importing
// here — types.ts is a single module, so nested references resolve within
// it automatically.
func usedTypeNames(p *protocol.Protocol, typeNames map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ref protocol.TypeRef) {
		root := rootNamedTypeID(ref)
		if root == "" {
			return
		}
		if !seen[root] {
			seen[root] = true
			out = append(out, typeNames[root])
		}
	}
	add(p.ConnectType)
	for _, m := range p.Methods {
		add(m.RequestType)
		add(m.ResponseType)
	}
	for _, e := range p.Events {
		add(e.PayloadType)
	}
	sort.Strings(out)
	return out
}

// rootNamedTypeID unwraps pointer/slice/array/map wrappers to find the
// named type ID a TypeRef ultimately refers to, if any.
func rootNamedTypeID(ref protocol.TypeRef) string {
	switch ref.Kind {
	case protocol.KindStruct, protocol.KindEnum:
		return ref.NamedType
	case protocol.KindPointer, protocol.KindSlice, protocol.KindArray:
		return rootNamedTypeID(*ref.Elem)
	case protocol.KindMap:
		return rootNamedTypeID(*ref.MapValue)
	default:
		return ""
	}
}
