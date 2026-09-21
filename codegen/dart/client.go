package dart

import (
	"fmt"
	"strings"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

func methodIndex(p *protocol.Protocol) map[string]protocol.Method {
	idx := make(map[string]protocol.Method, len(p.Methods))
	for _, m := range p.Methods {
		idx[m.Name] = m
	}
	return idx
}

func methodNames(p *protocol.Protocol) []string {
	out := make([]string, len(p.Methods))
	for i, m := range p.Methods {
		out[i] = m.Name
	}
	return out
}

func eventType(p *protocol.Protocol) (protocol.TypeRef, error) {
	ref, ok := p.EventRef()
	if !ok {
		return protocol.TypeRef{}, fmt.Errorf("protocol has no event type")
	}
	return ref, nil
}

// eventPropertyNames maps every event's full dotted name to its camelCase
// getter name on the generated events accessor, failing if two events
// collide once camelCased.
func eventPropertyNames(p *protocol.Protocol) (map[string]string, error) {
	out := make(map[string]string, len(p.Events))
	used := make(map[string]string, len(p.Events))
	for _, e := range p.Events {
		prop := names.CamelCase(e.Name)
		if owner, dup := used[prop]; dup {
			return nil, fmt.Errorf(
				"dart: events %q and %q both generate the property name %q; rename one of them",
				owner, e.Name, prop,
			)
		}
		used[prop] = e.Name
		out[e.Name] = prop
	}
	return out, nil
}

// namespaceClassName returns the generated Dart class name for the
// namespace node reached by path (e.g. ["billing", "invoice"] ->
// "_BillingInvoiceNamespace"), private to the generated library.
func namespaceClassName(clientName string, path []string) string {
	var b strings.Builder
	b.WriteString("_")
	b.WriteString(clientName)
	for _, seg := range path {
		b.WriteString(names.PascalCase(seg))
	}
	b.WriteString("Namespace")
	return b.String()
}

// collectNamespaceClasses walks the method tree and renders one private
// Dart class per non-leaf node (deepest first isn't required since each
// class only references its own children's already-known class names).
func collectNamespaceClasses(clientName, connectedName string, node *names.MethodNode, path []string, methods map[string]protocol.Method, typeNames map[string]string, out *[]string) {
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if !child.IsLeaf {
			collectNamespaceClasses(clientName, connectedName, child, append(path, seg), methods, typeNames, out)
		}
	}

	if len(path) == 0 {
		return // the root namespace is inlined directly onto the connected client, not its own class
	}

	className := namespaceClassName(clientName, path)
	var b strings.Builder
	fmt.Fprintf(&b, "class %s {\n", className)
	fmt.Fprintf(&b, "  final %s _client;\n\n", connectedName)
	fmt.Fprintf(&b, "  %s(this._client)", className)

	var childInits []string
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if !child.IsLeaf {
			childPath := append(append([]string(nil), path...), seg)
			childInits = append(childInits, fmt.Sprintf("%s = %s(_client)", names.CamelCase(seg), namespaceClassName(clientName, childPath)))
		}
	}

	if len(childInits) == 0 {
		b.WriteString(";\n\n")
	} else {
		b.WriteString(" :\n      ")
		b.WriteString(strings.Join(childInits, ",\n      "))
		b.WriteString(";\n\n")
	}

	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		propName := names.CamelCase(seg)
		if child.IsLeaf {
			m := methods[child.FullName]
			respType := dartType(m.ResponseType, typeNames)
			reqType := dartType(m.RequestType, typeNames)
			fmt.Fprintf(&b, "  Future<%s> %s(%s req) => _client.call(\n", respType, propName, reqType)
			fmt.Fprintf(&b, "        %q,\n", child.FullName)
			b.WriteString("        req.toBinary(),\n")
			fmt.Fprintf(&b, "        (raw) => %s,\n", decodeExpr("raw", m.ResponseType, typeNames))
			b.WriteString("      );\n\n")
		} else {
			childPath := append(append([]string(nil), path...), seg)
			fmt.Fprintf(&b, "  final %s %s;\n", namespaceClassName(clientName, childPath), propName)
		}
	}
	b.WriteString("}\n\n")

	*out = append(*out, b.String())
}

// renderRootMembers renders the top-level namespace/method fields declared
// directly on the Connected*Client class (the root of the method tree).
func renderRootMembers(clientName string, root *names.MethodNode, methods map[string]protocol.Method, typeNames map[string]string) string {
	var b strings.Builder
	for _, seg := range root.ChildOrder {
		child := root.Children[seg]
		propName := names.CamelCase(seg)
		if child.IsLeaf {
			m := methods[child.FullName]
			respType := dartType(m.ResponseType, typeNames)
			reqType := dartType(m.RequestType, typeNames)
			fmt.Fprintf(&b, "  Future<%s> %s(%s req) => call(\n", respType, propName, reqType)
			fmt.Fprintf(&b, "        %q,\n", child.FullName)
			b.WriteString("        req.toBinary(),\n")
			fmt.Fprintf(&b, "        (raw) => %s,\n", decodeExpr("raw", m.ResponseType, typeNames))
			b.WriteString("      );\n\n")
		} else {
			fmt.Fprintf(&b, "  late final %s %s = %s(this);\n", namespaceClassName(clientName, []string{seg}), propName, namespaceClassName(clientName, []string{seg}))
		}
	}
	return b.String()
}

func renderEventFields(p *protocol.Protocol, typeNames map[string]string) string {
	var b strings.Builder
	for _, e := range p.Events {
		fieldName := "_" + names.CamelCase(e.Name) + "Controller"
		payloadType := dartType(e.PayloadType, typeNames)
		fmt.Fprintf(&b, "  final StreamController<%s> %s = StreamController<%s>.broadcast();\n", payloadType, fieldName, payloadType)
	}
	return b.String()
}

func renderDispatchEvent(p *protocol.Protocol, typeNames map[string]string) string {
	var b strings.Builder
	b.WriteString("  @override\n")
	b.WriteString("  void dispatchEvent(String? event, dynamic payload) {\n")
	b.WriteString("    switch (event) {\n")
	for _, e := range p.Events {
		fieldName := "_" + names.CamelCase(e.Name) + "Controller"
		fmt.Fprintf(&b, "      case %q:\n", e.Name)
		fmt.Fprintf(&b, "        %s.add(%s);\n", fieldName, decodeExpr("payload", e.PayloadType, typeNames))
		b.WriteString("        break;\n")
	}
	b.WriteString("      default:\n        break;\n")
	b.WriteString("    }\n  }\n")
	return b.String()
}

func renderEventsAccessorClass(clientName string, connectedName string, p *protocol.Protocol, eventProps map[string]string, typeNames map[string]string) (string, string) {
	className := clientName + "Events"
	var b strings.Builder
	fmt.Fprintf(&b, "class %s {\n", className)
	fmt.Fprintf(&b, "  final %s _client;\n\n", connectedName)
	fmt.Fprintf(&b, "  %s(this._client);\n\n", className)
	for _, e := range p.Events {
		payloadType := dartType(e.PayloadType, typeNames)
		fieldName := "_" + names.CamelCase(e.Name) + "Controller"
		fmt.Fprintf(&b, "  Stream<%s> get %s => _client.%s.stream;\n", payloadType, eventProps[e.Name], fieldName)
	}
	b.WriteString("}\n\n")
	return b.String(), className
}

// generateClientFile renders client.dart: the top-level "<Name>Client"
// (with a typed connect()) and "Connected<Name>Client" (the typed RPC/event
// surface) classes, plus one private namespace class per non-leaf method
// path segment.
func generateClientFile(p *protocol.Protocol, clientName string, typeNames map[string]string) (string, error) {
	eventRef, ok := p.EventRef()
	if !ok {
		return "", fmt.Errorf("protocol has no event type")
	}

	connectedName := "Connected" + clientName
	payloadType := dartType(eventRef, typeNames)
	var b strings.Builder
	b.WriteString("import 'dart:async';\n\n")
	b.WriteString("import 'models.dart';\n")
	b.WriteString("import 'runtime.dart';\n\n")

	fmt.Fprintf(&b, "class %s {\n", clientName)
	b.WriteString("  final ClientOptions options;\n\n")
	fmt.Fprintf(&b, "  %s(this.options);\n\n", clientName)
	fmt.Fprintf(&b, "  Future<%s> connect() async {\n", connectedName)
	fmt.Fprintf(&b, "    final handshake = await connectSocket(options.url, %q);\n", p.Version)
	fmt.Fprintf(&b, "    return %s(handshake);\n", connectedName)
	b.WriteString("  }\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "class %s extends BaseConnection {\n", connectedName)
	fmt.Fprintf(&b, "  final StreamController<%s> _eventsController = StreamController<%s>.broadcast();\n", payloadType, payloadType)
	fmt.Fprintf(&b, "  Stream<%s> get events => _eventsController.stream;\n\n", payloadType)
	fmt.Fprintf(&b, "  %s(HandshakeResult handshake) : super(handshake);\n\n", connectedName)
	for _, m := range p.Methods {
		respType := dartType(m.ResponseType, typeNames)
		reqType := dartType(m.RequestType, typeNames)
		fmt.Fprintf(&b, "  Future<%s> %s(%s req) => call(\n", respType, names.CamelCase(m.Name), reqType)
		fmt.Fprintf(&b, "        %q,\n", m.Name)
		b.WriteString("        req.toBinary(),\n")
		fmt.Fprintf(&b, "        (raw) => %s,\n", decodeExpr("raw", m.ResponseType, typeNames))
		b.WriteString("      );\n\n")
	}
	b.WriteString("  @override\n")
	fmt.Fprintf(&b, "  void dispatchEvent(dynamic payload) {\n    _eventsController.add(%s);\n  }\n", decodeExpr("payload", eventRef, typeNames))
	b.WriteString("}\n\n")
	return b.String(), nil
}
