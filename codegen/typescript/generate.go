// Package typescript generates a production-quality, strict-mode-clean
// TypeScript client from Latchwire's protocol IR. Output is a
// self-contained set of files (runtime.ts, types.ts, client.ts, index.ts):
// see docs/design-notes.md ("Runtime/generated code split") for why the
// runtime half is not yet its own npm package.
package typescript

import (
	"fmt"

	"github.com/vehmloewff/latchwire/codegen"
	"github.com/vehmloewff/latchwire/names"
	"github.com/vehmloewff/latchwire/protocol"
)

// Options configures TypeScript generation.
type Options struct {
	// ClientName is the base name for the generated classes: "<Name>Client"
	// and "Connected<Name>Client". Defaults to "LatchwireClient".
	ClientName string
}

func (o Options) clientName(_ *protocol.Protocol) string {
	if o.ClientName != "" {
		return o.ClientName
	}
	return "LatchwireClient"
}

// Generate renders a complete TypeScript client from p, returning a map of
// relative file path to file contents. It is deterministic: the same
// protocol always produces byte-identical output.
func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	if err := names.ValidateIdentifiers(methodNames(p)); err != nil {
		return nil, fmt.Errorf("typescript: %w", err)
	}
	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("typescript: %w", err)
	}

	header := codegen.HeaderComment(p.Version)
	clientName := opts.clientName(p)

	clientBody, err := generateClientFile(p, clientName, typeNames)
	if err != nil {
		return nil, fmt.Errorf("typescript: %w", err)
	}

	files := map[string][]byte{
		"runtime.ts": []byte(header + runtimeBody),
		"types.ts":   []byte(header + generateTypesFile(p, typeNames)),
		"client.ts":  []byte(header + clientBody),
		"index.ts":   []byte(header + generateIndexFile(clientName)),
	}
	return files, nil
}

func generateIndexFile(clientName string) string {
	return fmt.Sprintf(
		"export { %s, Connected%s } from \"./client\";\nexport { LatchwireError } from \"./runtime\";\nexport type { ClientOptions, WebSocketFactory, WebSocketLike } from \"./runtime\";\nexport * from \"./types\";\n",
		clientName, clientName,
	)
}
