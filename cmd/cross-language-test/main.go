// Command cross-language-test discovers every example under examples/, runs its
// generator, performs language static checks, starts its Go server, and runs
// the checked-in language integration programs. It is intentionally outside
// the normal Go test suite.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

const root = "."

type example struct {
	name string
	dir  string
}

func run(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic(fmt.Errorf("%s %v: %w", name, args, err))
	}
}

func main() {
	selected := languages(os.Args[1:])
	examples := discoverExamples()
	if len(examples) == 0 {
		panic("no examples with both gen/ and server/ directories found")
	}
	for i, ex := range examples {
		runExample(ex, i, selected)
	}
}

func discoverExamples() []example {
	entries, err := os.ReadDir(filepath.Join(root, "examples"))
	if err != nil {
		panic(err)
	}
	var result []example
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, "examples", entry.Name())
		if isDir(filepath.Join(dir, "cmd", "generate_clients")) && isDir(filepath.Join(dir, "cmd", "server")) {
			result = append(result, example{name: entry.Name(), dir: dir})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

func runExample(ex example, index int, selected map[string]bool) {
	fmt.Printf("\n=== example: %s ===\n", ex.name)
	run(ex.dir, "go", "run", "./cmd/generate_clients")

	ts := filepath.Join(ex.dir, "typescript")
	dart := filepath.Join(ex.dir, "dart")

	// Go is compiled and tested after the example server starts below.
	if selected["typescript"] {
		run(ts, "npm", "install")
		run(ts, "npm", "run", "check")
	}
	if selected["dart"] {
		run(dart, "dart", "pub", "get")
		run(dart, "dart", "analyze", "lib", "main.dart", "test")
	}
	if !selected["go"] && !selected["typescript"] && !selected["dart"] {
		return
	}

	port := 18080 + index
	address := "127.0.0.1:" + strconv.Itoa(port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tmp, err := os.MkdirTemp("", "latch-example-server-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	serverBinary := filepath.Join(tmp, "server")
	run(ex.dir, "go", "build", "-o", serverBinary, "./cmd/server")
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
		goTest.Dir, goTest.Env, goTest.Stdout, goTest.Stderr = ex.dir, env, os.Stdout, os.Stderr
		if err := goTest.Run(); err != nil {
			panic(fmt.Errorf("%s Go protocol tests: %w", ex.name, err))
		}
		runGo := exec.Command("go", "run", "./golang/example")
		runGo.Dir, runGo.Env, runGo.Stdout, runGo.Stderr = ex.dir, env, os.Stdout, os.Stderr
		if err := runGo.Run(); err != nil {
			panic(fmt.Errorf("%s Go example: %w", ex.name, err))
		}
	}
	if selected["typescript"] {
		node := exec.Command("npm", "test")
		node.Dir, node.Env, node.Stdout, node.Stderr = ts, env, os.Stdout, os.Stderr
		if err := node.Run(); err != nil {
			panic(fmt.Errorf("%s TypeScript protocol tests: %w", ex.name, err))
		}
		exampleRun := exec.Command("npm", "run", "example")
		exampleRun.Dir, exampleRun.Env, exampleRun.Stdout, exampleRun.Stderr = ts, env, os.Stdout, os.Stderr
		if err := exampleRun.Run(); err != nil {
			panic(fmt.Errorf("%s TypeScript example: %w", ex.name, err))
		}
	}
	if selected["dart"] {
		dartTests := exec.Command("dart", "test")
		dartTests.Dir, dartTests.Env, dartTests.Stdout, dartTests.Stderr = dart, env, os.Stdout, os.Stderr
		if err := dartTests.Run(); err != nil {
			panic(fmt.Errorf("%s Dart protocol tests: %w", ex.name, err))
		}
		dartMain := exec.Command("dart", "run", "main.dart")
		dartMain.Dir, dartMain.Env, dartMain.Stdout, dartMain.Stderr = dart, env, os.Stdout, os.Stderr
		if err := dartMain.Run(); err != nil {
			panic(fmt.Errorf("%s Dart example: %w", ex.name, err))
		}
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func languages(args []string) map[string]bool {
	valid := map[string]bool{"go": true, "dart": true, "typescript": true}
	selected := map[string]bool{}
	if len(args) == 0 {
		for language := range valid {
			selected[language] = true
		}
		return selected
	}
	for _, language := range args {
		if !valid[language] {
			panic(fmt.Sprintf("unknown language %q; choose go, dart, or typescript", language))
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
