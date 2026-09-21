// Package jsonschema converts Latch's protocol IR (package protocol)
// into JSON Schema 2020-12 documents, and compiles those documents into
// validators for runtime use. It is the only package that knows about JSON
// Schema keywords; nothing else in Latch hand-builds schema documents.
package jsonschema

import (
	"fmt"
	"strings"

	"github.com/vehmloewff/latch/protocol"
)

const draft202012 = "https://json-schema.org/draft/2020-12/schema"

// BuildDocument produces a complete, self-contained JSON Schema document for
// root, including a "$defs" section for every named type it (transitively)
// references. root must resolve to a named struct or enum type — Latch
// requires method requests/responses and the event payload to all be named
// struct types.
func BuildDocument(p *protocol.Protocol, root protocol.TypeRef) map[string]any {
	defs := map[string]any{}
	seen := map[string]bool{}

	rootSchema := typeSchema(p, root, nil, defs, seen)

	doc := map[string]any{"$schema": draft202012}
	for k, v := range rootSchema {
		doc[k] = v
	}
	if len(defs) > 0 {
		doc["$defs"] = defs
	}
	return doc
}

// AllDefs returns a JSON Schema "$defs" map covering every named type in p,
// keyed by type ID. Unlike BuildDocument, which only includes types
// reachable from one root, this covers the whole protocol at once — used by
// the protocol manifest, which exposes every named type a single time
// rather than duplicating it per method/event.
func AllDefs(p *protocol.Protocol) map[string]any {
	defs := map[string]any{}
	seen := map[string]bool{}
	for _, t := range p.Types {
		addDef(p, t.ID, defs, seen)
	}
	return defs
}

func typeSchema(p *protocol.Protocol, ref protocol.TypeRef, c *protocol.Constraints, defs map[string]any, seen map[string]bool) map[string]any {
	switch ref.Kind {
	case protocol.KindString:
		s := map[string]any{"type": "string"}
		applyConstraints(s, c)
		return s

	case protocol.KindBool:
		return map[string]any{"type": "boolean"}

	case protocol.KindInt, protocol.KindInt8, protocol.KindInt16, protocol.KindInt32, protocol.KindInt64,
		protocol.KindUint, protocol.KindUint8, protocol.KindUint16, protocol.KindUint32, protocol.KindUint64:
		s := map[string]any{"type": "integer"}
		applyConstraints(s, c)
		return s

	case protocol.KindFloat32, protocol.KindFloat64:
		s := map[string]any{"type": "number"}
		applyConstraints(s, c)
		return s

	case protocol.KindTime:
		return map[string]any{"type": "string", "format": "date-time"}

	case protocol.KindPointer:
		inner := typeSchema(p, *ref.Elem, c, defs, seen)
		return map[string]any{"anyOf": []any{
			map[string]any{"type": "null"},
			inner,
		}}

	case protocol.KindSlice:
		return map[string]any{
			"type":  "array",
			"items": typeSchema(p, *ref.Elem, nil, defs, seen),
		}

	case protocol.KindArray:
		return map[string]any{
			"type":     "array",
			"items":    typeSchema(p, *ref.Elem, nil, defs, seen),
			"minItems": ref.ArrayLen,
			"maxItems": ref.ArrayLen,
		}

	case protocol.KindMap:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": typeSchema(p, *ref.MapValue, nil, defs, seen),
		}

	case protocol.KindStruct, protocol.KindEnum:
		addDef(p, ref.NamedType, defs, seen)
		return map[string]any{"$ref": "#/$defs/" + escapeRef(ref.NamedType)}

	default:
		panic(fmt.Sprintf("jsonschema: unhandled kind %q", ref.Kind))
	}
}

// addDef materializes the schema for named type id into defs, keyed by its
// raw (unescaped) ID — a "$defs" key is a plain JSON object property name,
// not a JSON Pointer, so only references to it (built with escapeRef) need
// "~0"/"~1" escaping.
func addDef(p *protocol.Protocol, id string, defs map[string]any, seen map[string]bool) {
	if seen[id] {
		return
	}
	seen[id] = true

	nt := p.TypeByID(id)
	if nt == nil {
		panic(fmt.Sprintf("jsonschema: unknown named type %q", id))
	}

	switch nt.Kind {
	case protocol.KindEnum:
		values := make([]any, len(nt.EnumValues))
		for i, v := range nt.EnumValues {
			values[i] = v
		}
		defs[id] = map[string]any{
			"type": "string",
			"enum": values,
		}

	case protocol.KindStruct:
		properties := map[string]any{}
		var required []string
		for _, f := range nt.Fields {
			c := f.Constraints
			properties[f.JSONName] = typeSchema(p, f.Type, &c, defs, seen)
			if !f.Optional {
				required = append(required, f.JSONName)
			}
		}
		def := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			def["required"] = required
		}
		defs[id] = def

	default:
		panic(fmt.Sprintf("jsonschema: named type %q has unsupported kind %q", id, nt.Kind))
	}
}

func applyConstraints(s map[string]any, c *protocol.Constraints) {
	if c == nil {
		return
	}
	if c.MinLength != nil {
		s["minLength"] = *c.MinLength
	}
	if c.MaxLength != nil {
		s["maxLength"] = *c.MaxLength
	}
	if c.Minimum != nil {
		s["minimum"] = *c.Minimum
	}
	if c.Maximum != nil {
		s["maximum"] = *c.Maximum
	}
	if c.Pattern != "" {
		s["pattern"] = c.Pattern
	}
	if c.Format != "" {
		s["format"] = c.Format
	}
}

// escapeRef encodes a Latch type ID (which may contain "/" from Go
// package paths) as a valid JSON Pointer token per RFC 6901.
func escapeRef(id string) string {
	id = strings.ReplaceAll(id, "~", "~0")
	id = strings.ReplaceAll(id, "/", "~1")
	return id
}
