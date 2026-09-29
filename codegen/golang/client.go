package golang

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
	fmt.Fprintf(&b, "type %s struct {\n\tsession *connectionSession\n", typeName)
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
		"func (%s *%s) %s(ctx context.Context, req %s) (%s, error) {\n\treturn call[%s](ctx, %s.session, %q, req)\n}\n\n",
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
		fmt.Fprintf(&b, "\t%s = &%s{session: session}\n", fieldExpr, typeName)
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
		fmt.Fprintf(&b, "\t%s = &%s{session: session}\n", childFieldExpr, typeName)
		b.WriteString(buildNamespaceInitChild(clientName, childFieldExpr, child, childPath))
	}
	return b.String()
}

// generateClientFile renders client.go with direct RPC methods and constructor callbacks.
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
	b.WriteString("import (\n\t\"context\"\n\t\"net/http\"\n\t\"sync\"\n\t\"time\"\n\n\t\"github.com/vehmloewff/latch/client\"\n)\n\n")

	fmt.Fprintf(&b, "// %s is a Latch client. Construct one with New,\n", clientName)
	fmt.Fprintf(&b, "// then call Connect to obtain a %s.\n", connectedName)
	fmt.Fprintf(&b, "type %s struct {\n\turl string\n\tonEvent func(%s)\n\tonConnectionStateChange func(ConnectionState)\n\tonRequestConstructed func(http.Header)\n}\n\n", clientName, eventGoType)
	b.WriteString("// ConnectionState describes the lifecycle of a connection.\ntype ConnectionState string\n\nconst (\n\tConnectionStateConnecting ConnectionState = \"connecting\"\n\tConnectionStateOffline ConnectionState = \"offline\"\n\tConnectionStateConnected ConnectionState = \"connected\"\n)\n\n")
	fmt.Fprintf(&b, "// New creates a %s targeting the given WebSocket URL. onConnectionStateChange is optional.\n", clientName)
	fmt.Fprintf(&b, "func New(url string, onEvent func(%s), onConnectionStateChange ...func(ConnectionState)) *%s {\n", eventGoType, clientName)
	b.WriteString("\tvar onState func(ConnectionState)\n\tif len(onConnectionStateChange) > 0 {\n\t\tonState = onConnectionStateChange[0]\n\t}\n")
	b.WriteString("\treturn NewWithOptions(url, onEvent, Options{OnConnectionStateChange: onState})\n}\n\n")
	b.WriteString("// Options configures connection callbacks. OnRequestConstructed receives fresh HTTP headers\n// before every WebSocket dial, including retries and reconnects.\ntype Options struct {\n\tOnConnectionStateChange func(ConnectionState)\n\tOnRequestConstructed func(http.Header)\n}\n\n")
	fmt.Fprintf(&b, "// NewWithOptions creates a %s with optional connection callbacks.\n", clientName)
	fmt.Fprintf(&b, "func NewWithOptions(url string, onEvent func(%s), opts Options) *%s {\n", eventGoType, clientName)
	fmt.Fprintf(&b, "\treturn &%s{url: url, onEvent: onEvent, onConnectionStateChange: opts.OnConnectionStateChange, onRequestConstructed: opts.OnRequestConstructed}\n}\n\n", clientName)

	fmt.Fprintf(&b, "// Connect opens a live %s.\n", connectedName)
	fmt.Fprintf(&b, "func (c *%s) Connect(ctx context.Context) (*%s, error) {\n", clientName, connectedName)
	b.WriteString("\tsession := &connectionSession{changed: make(chan struct{}), done: make(chan struct{}), onState: c.onConnectionStateChange, onRequestConstructed: c.onRequestConstructed}\n\tsession.setState(ConnectionStateConnecting)\n")
	fmt.Fprintf(&b, "\tconn, err := session.dial(ctx, c.url, %q)\n", p.Version)
	b.WriteString("\tif err != nil {\n\t\tsession.setState(ConnectionStateOffline)\n\t\treturn nil, err\n\t}\n\n")
	fmt.Fprintf(&b, "\tresult := &%s{session: session}\n", connectedName)
	fmt.Fprintf(&b, "\tready := make(chan struct{})\n\tgo session.run(conn, c.url, %q, ready, func(conn *client.Conn) {\n\t\tevents := client.RegisterEvent[%s](conn)\n\t\tgo func() {\n\t\t\tfor event := range events {\n\t\t\t\tif c.onEvent != nil { c.onEvent(event) }\n\t\t\t}\n\t\t}()\n\t})\n\t<-ready\n", p.Version, eventGoType)
	b.WriteString("\treturn result, nil\n}\n\n")

	fmt.Fprintf(&b, "// %s is a live, connected %s client.\n", connectedName, clientName)
	fmt.Fprintf(&b, "type %s struct {\n\tsession *connectionSession\n", connectedName)
	b.WriteString("}\n\n")

	for _, m := range p.Methods {
		b.WriteString(renderMethodFunc(connectedName, "c", m.Name, methods[m.Name], typeNames))
	}

	fmt.Fprintf(&b, "// Close closes the connection.\n")
	fmt.Fprintf(&b, "func (c *%s) Close() error {\n\treturn c.session.close()\n}\n\n", connectedName)
	fmt.Fprintf(&b, "// Closed returns a channel that is closed once the connection has closed.\n")
	fmt.Fprintf(&b, "func (c *%s) Closed() <-chan struct{} {\n\treturn c.session.done\n}\n\n", connectedName)
	b.WriteString(connectionSessionRuntime)

	return b.String(), nil
}

// connectionSessionRuntime keeps generated clients usable across transport failures.
const connectionSessionRuntime = `// connectionSession owns the replaceable transport. Closed signals intentional shutdown only.
type connectionSession struct {
 mu sync.Mutex
 conn *client.Conn
 changed chan struct{}
 done chan struct{}
 stopped bool
 onState func(ConnectionState)
 onRequestConstructed func(http.Header)
}

func (s *connectionSession) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *connectionSession) setState(state ConnectionState) {
 if s.onState != nil { s.onState(state) }
}
func (s *connectionSession) dial(ctx context.Context, url, version string) (*client.Conn, error) {
 for {
  if err := ctx.Err(); err != nil { return nil, err }
  headers := make(http.Header)
  if s.onRequestConstructed != nil { s.onRequestConstructed(headers) }
  conn, err := client.Connect(ctx, url, version, headers)
  if err == nil { return conn, nil }
  if err := ctx.Err(); err != nil { return nil, err }
  s.setState(ConnectionStateOffline)
  timer := time.NewTimer(2 * time.Second)
  select {
  case <-ctx.Done(): timer.Stop(); return nil, ctx.Err()
  case <-s.done: timer.Stop(); return nil, &client.Error{Code: "connection_closed", Message: "the connection is closed"}
  case <-timer.C:
  }
  s.setState(ConnectionStateConnecting)
 }
}
func (s *connectionSession) run(conn *client.Conn, url, version string, ready chan struct{}, register func(*client.Conn)) {
 for {
  s.mu.Lock()
  if s.stopped { s.mu.Unlock(); _ = conn.Close(); if ready != nil { close(ready) }; return }
  register(conn)
  s.conn = conn
  s.signal()
  s.mu.Unlock()
  conn.Start()
  s.setState(ConnectionStateConnected)
  if ready != nil { close(ready); ready = nil }
  select {
  case <-conn.Closed():
  case <-s.done: return
  }
  s.mu.Lock()
  if s.stopped { s.mu.Unlock(); return }
  s.conn = nil
  s.signal()
  s.mu.Unlock()
  s.setState(ConnectionStateOffline)
  timer := time.NewTimer(2 * time.Second)
  select {
  case <-timer.C:
  case <-s.done: timer.Stop(); return
  }
  s.setState(ConnectionStateConnecting)
  next, err := s.dialUntilClosed(url, version)
  if err != nil { return }
  conn = next
 }
}
func (s *connectionSession) dialUntilClosed(url, version string) (*client.Conn, error) {
 ctx, cancel := context.WithCancel(context.Background())
 defer cancel()
 go func() { select { case <-s.done: cancel(); case <-ctx.Done(): } }()
 return s.dial(ctx, url, version)
}
func (s *connectionSession) close() error {
 s.mu.Lock()
 if s.stopped { s.mu.Unlock(); return nil }
 s.stopped = true
 close(s.done)
 s.signal()
 conn := s.conn
 s.conn = nil
 s.mu.Unlock()
 s.setState(ConnectionStateOffline)
 if conn != nil { return conn.Close() }
 return nil
}
func call[T any](ctx context.Context, s *connectionSession, method string, req any) (T, error) {
 var zero T
 for {
  s.mu.Lock()
  conn, changed, stopped := s.conn, s.changed, s.stopped
  s.mu.Unlock()
  if stopped { return zero, &client.Error{Code: "connection_closed", Message: "the connection is closed"} }
  if err := ctx.Err(); err != nil { return zero, err }
  if conn != nil {
   select {
   case <-conn.Closed(): // wait for the session to replace this socket
   default:
    // Once selected, this action is never retried, even if the socket dies during send.
    return client.Call[T](ctx, conn, method, req)
   }
  }
  select {
  case <-changed:
  case <-s.done: return zero, &client.Error{Code: "connection_closed", Message: "the connection is closed"}
  case <-ctx.Done(): return zero, ctx.Err()
  }
 }
}
`

func unexported(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
