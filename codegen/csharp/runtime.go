package csharp

import _ "embed"

// runtimeSource is inserted after the generated file's using directives.
//
//go:embed runtime.cs
var runtimeSource string
