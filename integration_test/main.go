// Command integration_test runs the chat app example's cross-language
// integration checks. It is intentionally outside the normal Go test suite.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	root              = "."
	exampleName       = "chat_app_example"
	exampleDir        = exampleName
	typescriptRuntime = "codegen/typescript/binary_runtime"
	dartRuntime       = "codegen/dart/binary_runtime"
)

func run(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic(fmt.Errorf("%s %v: %w", name, args, err))
	}
}

func main() {
	runExample(languages(os.Args[1:]))
}

func runExample(selected map[string]bool) {
	ex := filepath.Join(root, exampleDir)
	runBinaryRuntimeTests(selected)

	fmt.Printf("\n=== example: %s ===\n", exampleName)
	args := []string{"run", "./cmd/generate_clients"}
	for _, language := range []string{"go", "typescript", "dart", "swift", "kotlin", "rust"} {
		if selected[language] {
			args = append(args, language)
		}
	}
	run(ex, "go", args...)

	ts := filepath.Join(ex, "typescript")
	dart := filepath.Join(ex, "dart")

	// Go is compiled and tested after the example server starts below.
	if selected["typescript"] {
		run(ts, "npm", "ci")
		run(ts, "npm", "run", "check")
	}
	if selected["dart"] {
		run(dart, "dart", "pub", "get")
		run(dart, "dart", "analyze", "lib", "main.dart", "test")
	}
	if !selected["go"] && !selected["typescript"] && !selected["dart"] && !selected["swift"] && !selected["kotlin"] && !selected["rust"] {
		return
	}

	port := 18080
	address := "127.0.0.1:" + strconv.Itoa(port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tmp, err := os.MkdirTemp("", "latch-example-server-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	serverBinary := filepath.Join(tmp, "server")
	run(ex, "go", "build", "-o", serverBinary, "./cmd/server")
	server := exec.CommandContext(ctx, serverBinary)
	server.Dir = root
	server.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	server.Stdout, server.Stderr = os.Stdout, os.Stderr
	if err := server.Start(); err != nil {
		panic(err)
	}
	defer server.Process.Kill()
	waitForPort(address)

	env := append(os.Environ(), "SERVER_URL=ws://"+address+"/ws")
	if selected["go"] {
		goTest := exec.Command("go", "test", "./golang")
		goTest.Dir, goTest.Env, goTest.Stdout, goTest.Stderr = ex, env, os.Stdout, os.Stderr
		if err := goTest.Run(); err != nil {
			panic(fmt.Errorf("%s Go protocol tests: %w", exampleName, err))
		}
		runGo := exec.Command("go", "run", "./golang/example")
		runGo.Dir, runGo.Env, runGo.Stdout, runGo.Stderr = ex, env, os.Stdout, os.Stderr
		if err := runGo.Run(); err != nil {
			panic(fmt.Errorf("%s Go example: %w", exampleName, err))
		}
	}
	if selected["typescript"] {
		node := exec.Command("npm", "test")
		node.Dir, node.Env, node.Stdout, node.Stderr = ts, env, os.Stdout, os.Stderr
		if err := node.Run(); err != nil {
			panic(fmt.Errorf("%s TypeScript protocol tests: %w", exampleName, err))
		}
		exampleRun := exec.Command("npm", "run", "example")
		exampleRun.Dir, exampleRun.Env, exampleRun.Stdout, exampleRun.Stderr = ts, env, os.Stdout, os.Stderr
		if err := exampleRun.Run(); err != nil {
			panic(fmt.Errorf("%s TypeScript example: %w", exampleName, err))
		}
	}
	if selected["kotlin"] {
		run(root, "go", "test", "./codegen/kotlin", "-count=1")
		kotlinDir := filepath.Join(ex, "kotlin")
		jar := filepath.Join(tmp, "kotlin-integration.jar")
		run(kotlinDir, "kotlinc", "LatchClient.kt", "Integration.kt", "-include-runtime", "-d", jar)
		kotlinTest := exec.Command("java", "-jar", jar)
		kotlinTest.Dir, kotlinTest.Env, kotlinTest.Stdout, kotlinTest.Stderr = kotlinDir, env, os.Stdout, os.Stderr
		if err := kotlinTest.Run(); err != nil {
			panic(fmt.Errorf("%s Kotlin integration tests: %w", exampleName, err))
		}
	}
	if selected["swift"] {
		swiftDir := filepath.Join(ex, "swift")
		run(swiftDir, "swiftlint", "lint", "--strict")
		swiftTest := exec.Command("swift", "test", "-Xswiftc", "-warnings-as-errors")
		swiftTest.Dir, swiftTest.Env, swiftTest.Stdout, swiftTest.Stderr = swiftDir, env, os.Stdout, os.Stderr
		if err := swiftTest.Run(); err != nil {
			panic(fmt.Errorf("%s Swift tests: %w", exampleName, err))
		}
	}
	if selected["rust"] {
		rustTest := exec.Command("cargo", "test", "--offline")
		rustTest.Dir, rustTest.Env, rustTest.Stdout, rustTest.Stderr = filepath.Join(ex, "rust"), env, os.Stdout, os.Stderr
		if err := rustTest.Run(); err != nil {
			panic(fmt.Errorf("%s Rust integration tests: %w", exampleName, err))
		}
	}
	if selected["dart"] {
		dartTests := exec.Command("dart", "test")
		dartTests.Dir, dartTests.Env, dartTests.Stdout, dartTests.Stderr = dart, env, os.Stdout, os.Stderr
		if err := dartTests.Run(); err != nil {
			panic(fmt.Errorf("%s Dart protocol tests: %w", exampleName, err))
		}
		dartMain := exec.Command("dart", "run", "main.dart")
		dartMain.Dir, dartMain.Env, dartMain.Stdout, dartMain.Stderr = dart, env, os.Stdout, os.Stderr
		if err := dartMain.Run(); err != nil {
			panic(fmt.Errorf("%s Dart example: %w", exampleName, err))
		}
	}
}

func runBinaryRuntimeTests(selected map[string]bool) {
	if selected["typescript"] {
		fmt.Println("\n=== TypeScript binary runtime ===")
		run(filepath.Join(root, typescriptRuntime), "npm", "ci")
		run(filepath.Join(root, typescriptRuntime), "npm", "test")
	}
	if selected["dart"] {
		fmt.Println("\n=== Dart binary runtime ===")
		run(filepath.Join(root, dartRuntime), "dart", "pub", "get")
		run(filepath.Join(root, dartRuntime), "dart", "test")
	}
}

func languages(args []string) map[string]bool {
	valid := map[string]bool{"go": true, "dart": true, "typescript": true, "swift": true, "kotlin": true, "rust": true}
	selected := map[string]bool{}
	if len(args) == 0 {
		for language := range valid {
			selected[language] = true
		}
		return selected
	}
	for _, language := range args {
		if !valid[language] {
			panic(fmt.Sprintf("unknown language %q; choose go, dart, typescript, swift, kotlin, or rust", language))
		}
		selected[language] = true
	}
	return selected
}

func waitForPort(address string) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic("timed out waiting for Go example server")
}
