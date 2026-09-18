package golang

import (
	"fmt"
	"strings"

	"github.com/vehmloewff/latchwire/names"
	"github.com/vehmloewff/latchwire/protocol"
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

// eventGetterNames maps every event's full dotted name to its PascalCase
// getter method name on the generated events struct, failing if two events
// collide once PascalCased.
func eventGetterNames(p *protocol.Protocol) (map[string]string, error) {
	out := make(map[string]string, len(p.Events))
	used := make(map[string]string, len(p.Events))
	for _, e := range p.Events {
		name := names.PascalCase(e.Name)
		if owner, dup := used[name]; dup {
			return nil, fmt.Errorf(
				"golang: events %q and %q both generate the method name %q; rename one of them",
				owner, e.Name, name,
			)
		}
		used[name] = e.Name
		out[e.Name] = name
	}
	return out, nil
}

// namespaceTypeName returns the generated Go type name for the namespace
// node reached by path (e.g. ["billing", "invoice"] on client
// "BillingClient" -> "BillingClientBillingInvoiceNamespace").
func namespaceTypeName(clientName string, path []string) string {
	var b strings.Builder
	b.WriteString(clientName)
	for _, seg := range path {
		b.WriteString(names.PascalCase(seg))
	}
	b.WriteString("Namespace")
	return b.String()
}

// collectNamespaceTypes walks the method tree and renders one Go struct
// (with its RPC methods) per non-leaf node.
func collectNamespaceTypes(clientName string, node *names.MethodNode, path []string, methods map[string]protocol.Method, typeNames map[string]string, out *[]string) {
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if !child.IsLeaf {
			collectNamespaceTypes(clientName, child, append(path, seg), methods, typeNames, out)
		}
	}

	if len(path) == 0 {
		return // the root namespace's methods/fields live directly on the Connected*Client
	}

	typeName := namespaceTypeName(clientName, path)
	var b strings.Builder
	fmt.Fprintf(&b, "type %s struct {\n\tconn *client.Conn\n", typeName)
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if !child.IsLeaf {
			childPath := append(append([]string(nil), path...), seg)
			fmt.Fprintf(&b, "\t%s *%s\n", names.PascalCase(seg), namespaceTypeName(clientName, childPath))
		}
	}
	b.WriteString("}\n\n")

	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if child.IsLeaf {
			m := methods[child.FullName]
			b.WriteString(renderMethodFunc(typeName, "n", child.FullName, m, typeNames))
		}
	}

	*out = append(*out, b.String())
}

func renderMethodFunc(receiverType, receiverName, fullMethodName string, m protocol.Method, typeNames map[string]string) string {
	funcName := names.PascalCase(lastSegment(fullMethodName))
	reqType := goType(m.RequestType, typeNames)
	respType := goType(m.ResponseType, typeNames)
	return fmt.Sprintf(
		"func (%s *%s) %s(ctx context.Context, req %s) (%s, error) {\n\treturn client.Call[%s](ctx, %s.conn, %q, req)\n}\n\n",
		receiverName, receiverType, funcName, reqType, respType, respType, receiverName, fullMethodName,
	)
}

func lastSegment(dotted string) string {
	segs := names.Segments(dotted)
	return segs[len(segs)-1]
}

// buildNamespaceInit emits the statements, inside Connect, that build every
// namespace struct (including nested ones) and assign it to its parent
// field (or, at the root, directly onto conn).
func buildNamespaceInit(clientName, varName string, node *names.MethodNode, path []string) string {
	var b strings.Builder
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if child.IsLeaf {
			continue
		}
		childPath := append(append([]string(nil), path...), seg)
		typeName := namespaceTypeName(clientName, childPath)
		fieldExpr := fmt.Sprintf("%s.%s", varName, names.PascalCase(seg))
		fmt.Fprintf(&b, "\t%s = &%s{conn: conn}\n", fieldExpr, typeName)
		b.WriteString(buildNamespaceInitChild(clientName, fieldExpr, child, childPath))
	}
	return b.String()
}

// buildNamespaceInitChild is like buildNamespaceInit but for a namespace
// struct already assigned to fieldExpr, rather than the root Connected*Client.
func buildNamespaceInitChild(clientName, fieldExpr string, node *names.MethodNode, path []string) string {
	var b strings.Builder
	for _, seg := range node.ChildOrder {
		child := node.Children[seg]
		if child.IsLeaf {
			continue
		}
		childPath := append(append([]string(nil), path...), seg)
		typeName := namespaceTypeName(clientName, childPath)
		childFieldExpr := fmt.Sprintf("%s.%s", fieldExpr, names.PascalCase(seg))
		fmt.Fprintf(&b, "\t%s = &%s{conn: conn}\n", childFieldExpr, typeName)
		b.WriteString(buildNamespaceInitChild(clientName, childFieldExpr, child, childPath))
	}
	return b.String()
}

// generateClientFile renders client.go with direct RPC methods and one typed
// server-event stream on the connected client.
func generateClientFile(pkg string, p *protocol.Protocol, clientName string, typeNames map[string]string) (string, error) {
	eventRef, err := eventType(p)
	if err != nil {
		return "", err
	}

	methods := methodIndex(p)
	connectedName := "Connected" + clientName
	eventGoType := goType(eventRef, typeNames)

	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("import (\n\t\"context\"\n\n\t\"github.com/vehmloewff/latchwire/client\"\n)\n\n")

	fmt.Fprintf(&b, "// %s is a Latchwire client. Construct one with New,\n", clientName)
	fmt.Fprintf(&b, "// then call Connect to obtain a %s.\n", connectedName)
	fmt.Fprintf(&b, "type %s struct {\n\turl string\n}\n\n", clientName)
	fmt.Fprintf(&b, "// New creates a %s targeting the given WebSocket URL.\n", clientName)
	fmt.Fprintf(&b, "func New(url string) *%s {\n\treturn &%s{url: url}\n}\n\n", clientName, clientName)

	fmt.Fprintf(&b, "// Connect opens a live %s.\n", connectedName)
	fmt.Fprintf(&b, "func (c *%s) Connect(ctx context.Context) (*%s, error) {\n", clientName, connectedName)
	fmt.Fprintf(&b, "\tconn, err := client.Connect(ctx, c.url, %q)\n", p.Version)
	b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n\n")
	fmt.Fprintf(&b, "\tresult := &%s{conn: conn, events: client.RegisterEvent[%s](conn)}\n", connectedName, eventGoType)
	b.WriteString("\tconn.Start()\n")
	b.WriteString("\treturn result, nil\n}\n\n")

	fmt.Fprintf(&b, "// %s is a live, connected %s client.\n", connectedName, clientName)
	fmt.Fprintf(&b, "type %s struct {\n\tconn *client.Conn\n\tevents <-chan %s\n", connectedName, eventGoType)
	b.WriteString("}\n\n")

	for _, m := range p.Methods {
		b.WriteString(renderMethodFunc(connectedName, "c", m.Name, methods[m.Name], typeNames))
	}
	fmt.Fprintf(&b, "// Events returns the single server-to-client event stream.\n")
	fmt.Fprintf(&b, "func (c *%s) Events() <-chan %s {\n\treturn c.events\n}\n\n", connectedName, eventGoType)

	fmt.Fprintf(&b, "// Close closes the connection.\n")
	fmt.Fprintf(&b, "func (c *%s) Close() error {\n\treturn c.conn.Close()\n}\n\n", connectedName)
	fmt.Fprintf(&b, "// Closed returns a channel that is closed once the connection has closed.\n")
	fmt.Fprintf(&b, "func (c *%s) Closed() <-chan struct{} {\n\treturn c.conn.Closed()\n}\n\n", connectedName)

	return b.String(), nil
}

func unexported(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
