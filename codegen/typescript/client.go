package typescript

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// methodIndex maps a wire method name to its IR definition.
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
				"(req: %s): Promise<%s> => this.call<%s>(%q, req, value => encodeTyped(value, %s, __latchWireTypes), data => decodeTyped(data, %s, __latchWireTypes) as %s)",
				tsType(m.RequestType, typeNames), tsType(m.ResponseType, typeNames), tsType(m.ResponseType, typeNames), child.FullName,
				wireTypeExpr(m.RequestType, typeNames), wireTypeExpr(m.ResponseType, typeNames), tsType(m.ResponseType, typeNames),
			))
		} else {
			b.WriteString(renderMethodNamespace(child, methods, typeNames, inner))
		}
		b.WriteString(",\n")
	}
	b.WriteString(indent)
	b.WriteByte('}')
	return b.String()
}

// renderClientFields renders every registered identifier as a direct method
// on the generated Connected*Client.
func renderClientFields(p *protocol.Protocol, typeNames map[string]string) string {
	var b strings.Builder
	for _, m := range p.Methods {
		b.WriteString(fmt.Sprintf(
			"  %s(req: %s): Promise<%s> {\n    return this.call<%s>(%q, req, value => encodeTyped(value, %s, __latchWireTypes), data => decodeTyped(data, %s, __latchWireTypes) as %s);\n  }\n",
			names.CamelCase(m.Name),
			tsType(m.RequestType, typeNames), tsType(m.ResponseType, typeNames), tsType(m.ResponseType, typeNames), m.Name,
			wireTypeExpr(m.RequestType, typeNames), wireTypeExpr(m.ResponseType, typeNames), tsType(m.ResponseType, typeNames),
		))
	}
	return b.String()
}

func renderDispatchEvent(eventType, eventTypeWireType string) string {
	var b strings.Builder
	b.WriteString("  protected dispatchEvent(env: { payload?: Uint8Array }): void {\n")
	fmt.Fprintf(&b, "    this.onEvent(decodeTyped(env.payload ?? new Uint8Array(), %s, __latchWireTypes) as %s);\n", eventTypeWireType, eventType)
	b.WriteString("  }\n")
	return b.String()
}

// generateClientFile renders client.ts: the top-level "<Name>Client" (with
// a typed connect()) and "Connected<Name>Client" (the typed RPC/event
// surface) classes.
func generateClientFile(p *protocol.Protocol, clientName string, typeNames map[string]string) (string, error) {
	eventRef, ok := p.EventRef()
	if !ok {
		return "", fmt.Errorf("protocol has no event type")
	}
	eventType := tsType(eventRef, typeNames)

	connectedName := "Connected" + clientName

	var b strings.Builder

	typeImports := usedTypeNames(p, typeNames)
	if len(typeImports) > 0 {
		b.WriteString(fmt.Sprintf("import type { %s } from \"./types\";\n", strings.Join(typeImports, ", ")))
	}
	b.WriteString("import { BaseConnection, type ClientOptions, connectSocket } from \"./runtime\";\n\n")

	fmt.Fprintf(&b, "export class %s {\n", clientName)
	fmt.Fprintf(&b, "  private readonly version = %q;\n", p.Version)
	fmt.Fprintf(&b, "  private options: ClientOptions & { onEvent: (event: %s) => void };\n", eventType)
	b.WriteString("  private state: ConnectionState = ConnectionState.Offline;\n  private stopped = false;\n  private retryTimer: ReturnType<typeof setTimeout> | undefined;\n  private wakeRetry: (() => void) | undefined;\n  private connecting = false;\n  private retrying = false;\n  private attempt?: AbortController;\n  private client?: BaseConnection;\n\n")
	fmt.Fprintf(&b, "  constructor(options: ClientOptions & { onEvent: (event: %s) => void }) {\n    this.options = options;\n  }\n\n", eventType)
	b.WriteString("  private setState(state: ConnectionState): void {\n    if (this.state === state) return;\n    this.state = state;\n    this.options.onConnectionStateChange?.(state);\n  }\n\n")
	fmt.Fprintf(&b, "  async connect(): Promise<%s> {\n", connectedName)
	b.WriteString("    if (this.connecting || this.client || this.stopped) {\n      throw new Error(\"Latch: client is already connecting or connected (or closed)\");\n    }\n    this.connecting = true;\n    this.setState(ConnectionState.Connecting);\n    while (!this.stopped) {\n    try {\n")
	fmt.Fprintf(&b, "      this.attempt = new AbortController();\n      const handshake = await connectSocket(this.options.url, this.options.webSocketFactory, %q, this.attempt.signal);\n", p.Version)
	fmt.Fprintf(&b, "      const client = new %s(handshake, this.options.onEvent, () => this.disconnected(), () => this.stop());\n", connectedName)
	b.WriteString("      if (this.stopped) { client.close(); break; }\n      this.client = client;\n      this.connecting = false;\n      this.setState(ConnectionState.Connected);\n      return client;\n    } catch (_) {\n      if (this.stopped) break;\n      this.setState(ConnectionState.Offline);\n      await this.delay();\n      if (!this.stopped) this.setState(ConnectionState.Connecting);\n    }\n    }\n    throw new LatchError(\"connection_closed\", \"the connection is closed\");\n")
	b.WriteString("  }\n\n  /** Cancel initial connection attempts or permanently close the active client. */\n  close(): void {\n    this.stop();\n    this.client?.close();\n  }\n\n  private stop(): void {\n    if (this.stopped) return;\n    this.stopped = true;\n    this.attempt?.abort();\n    this.wakeRetry?.();\n    this.setState(ConnectionState.Offline);\n  }\n\n  private delay(): Promise<void> {\n    return new Promise(resolve => {\n      this.wakeRetry = () => { if (this.retryTimer) clearTimeout(this.retryTimer); this.wakeRetry = undefined; resolve(); };\n      this.retryTimer = setTimeout(() => this.wakeRetry?.(), 2000);\n    });\n  }\n\n  private disconnected(): void {\n    this.setState(ConnectionState.Offline);\n    if (this.stopped || !this.client || this.retrying) return;\n    this.retrying = true;\n    void this.retry();\n  }\n\n  private async retry(): Promise<void> {\n    await this.delay();\n    while (!this.stopped) {\n      this.setState(ConnectionState.Connecting);\n      try {\n        this.attempt = new AbortController();\n        const handshake = await connectSocket(this.options.url, this.options.webSocketFactory, this.version, this.attempt.signal);\n        if (this.stopped) { handshake.ws.close(); return; }\n        if (!this.client?.reconnect(handshake)) {\n          if (this.stopped || this.client?.closed) return;\n          this.setState(ConnectionState.Offline);\n          await this.delay();\n          continue;\n        }\n        this.retrying = false;\n        this.setState(ConnectionState.Connected);\n        return;\n      } catch (_) {\n        if (this.stopped) return;\n        this.setState(ConnectionState.Offline);\n        await this.delay();\n      }\n    }\n  }\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "export class %s extends BaseConnection {\n", connectedName)
	b.WriteString(renderClientFields(p, typeNames))
	b.WriteString("\n")
	fmt.Fprintf(&b, "  private readonly onEvent: (event: %s) => void;\n\n", eventType)
	fmt.Fprintf(&b, "  constructor(handshake: HandshakeResult, onEvent: (event: %s) => void, onClose: () => void, onShutdown: () => void = () => {}) {\n    super(handshake, onClose, onShutdown);\n    this.onEvent = onEvent;\n  }\n\n", eventType)
	b.WriteString(renderDispatchEvent(eventType, wireTypeExpr(eventRef, typeNames)))
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
	for _, m := range p.Methods {
		add(m.RequestType)
		add(m.ResponseType)
	}
	if eventRef, ok := p.EventRef(); ok {
		add(eventRef)
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
