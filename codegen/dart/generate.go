// Package dart generates an idiomatic, null-safe Dart client from
// Latch's protocol IR. Output is a small, ready-to-use Dart package:
// pubspec.yaml, a public barrel file (lib/<package>.dart), and the
// implementation under lib/src/ (runtime.dart, models.dart, client.dart).
// See docs/design-notes.md ("Runtime/generated code split", "Dart optional
// vs nullable", "Dart unknown enum values") for the decisions behind this
// shape.
package dart

import (
	"fmt"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// Options configures Dart generation.
type Options struct {
	// Package is the pubspec package name and the name of the public
	// barrel file (lib/<Package>.dart). Defaults to "latch_client".
	Package string

	// ClientName is the base name for the generated classes: "<Name>Client"
	// and "Connected<Name>Client". Defaults to "LatchClient".
	ClientName string

	// WebSocketChannelVersion pins the package:web_socket_channel version
	// constraint written into pubspec.yaml. Defaults to "^3.0.0".
	WebSocketChannelVersion string
}

func (o Options) resolve(_ *protocol.Protocol) Options {
	out := o
	if out.Package == "" {
		out.Package = "latch_client"
	}
	if out.ClientName == "" {
		out.ClientName = "LatchClient"
	}
	if out.WebSocketChannelVersion == "" {
		out.WebSocketChannelVersion = "^3.0.0"
	}
	return out
}

// Generate renders a complete Dart package from p, returning a map of
// relative file path (from the package root) to file contents. It is
// deterministic: the same protocol always produces byte-identical output.
func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	if err := names.ValidateIdentifiers(methodNames(p)); err != nil {
		return nil, fmt.Errorf("dart: %w", err)
	}
	opts = opts.resolve(p)

	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("dart: %w", err)
	}

	header := codegen.HeaderComment(p.Version)

	clientBody, err := generateClientFile(p, opts.ClientName, typeNames)
	if err != nil {
		return nil, fmt.Errorf("dart: %w", err)
	}

	files := map[string][]byte{
		"pubspec.yaml":                  []byte(generatePubspec(opts)),
		"lib/" + opts.Package + ".dart": []byte(header + generateBarrelFile(opts.ClientName)),
		"lib/src/runtime.dart":          []byte(header + runtimeBody),
		"lib/src/models.dart":           []byte(header + generateModelsFile(p, typeNames)),
		"lib/src/client.dart":           []byte(header + clientBody),
	}
	return files, nil
}

func generatePubspec(opts Options) string {
	return fmt.Sprintf(`name: %s
description: Generated Latch client. DO NOT EDIT.
publish_to: "none"
version: 0.1.0

environment:
  sdk: ^3.0.0

dependencies:
  web_socket_channel: %s

dev_dependencies:
  test: ^1.25.0
`, opts.Package, opts.WebSocketChannelVersion)
}

func generateBarrelFile(clientName string) string {
	return fmt.Sprintf(
		"export 'src/client.dart' show %s, Connected%s;\n"+
			"export 'src/models.dart';\n"+
			"export 'src/runtime.dart' show ClientOptions, LatchError, LatchDecodeException;\n",
		clientName, clientName,
	)
}
