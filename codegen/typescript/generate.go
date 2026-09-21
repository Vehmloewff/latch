// Package typescript generates a production-quality, strict-mode-clean
// TypeScript client from Latch's protocol IR. Output is a
// self-contained set of files (runtime.ts, types.ts, client.ts, index.ts):
// see docs/design-notes.md ("Runtime/generated code split") for why the
// runtime half is not yet its own npm package.
package typescript

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// binaryRuntimeBody is the tested standalone codec embedded into every generated
// client. Generated clients never import the development subproject.
//
//go:embed binary_runtime/codec.ts
var binaryRuntimeBody string

// Options configures TypeScript generation.
type Options struct {
	// ClientName is the base name for the generated classes: "<Name>Client"
	// and "Connected<Name>Client". Defaults to "LatchClient".
	ClientName string
}

func (o Options) clientName(_ *protocol.Protocol) string {
	if o.ClientName != "" {
		return o.ClientName
	}
	return "LatchClient"
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

	// Keep runtime, protocol types, and the client in one importable source
	// file. The generated sections are intentionally ordered so declarations
	// are available before the client uses them.
	typesBody := generateTypesFile(p, typeNames)
	clientBody = stripImports(clientBody)
	return map[string][]byte{
		"client.ts": []byte(header + runtimeBody + "\n" + binaryRuntimeBody + "\n" + typesBody + "\n" + clientBody),
	}, nil
}

func stripImports(src string) string {
	var lines []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "import ") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func generateIndexFile(clientName string) string {
	return fmt.Sprintf(
		"export { %s, Connected%s } from \"./client\";\nexport { LatchError } from \"./runtime\";\nexport type { ClientOptions, WebSocketFactory, WebSocketLike } from \"./runtime\";\nexport * from \"./types\";\n",
		clientName, clientName,
	)
}
