// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// redis_command_total names a command only when redisCommandNames lists it;
// anything else is "other". That fold is for a command this build does not
// issue, and the store census's RANDOMKEY and MEMORY USAGE - two thousand
// commands a census on each store it weighs - sat there with every other
// command the code issues and the list left out.
//
// This reads the go-redis client the module builds against for the commands
// each of its methods sends, then every non-test file of alarmd for the
// client methods it calls with a context and the raw commands it sends by
// name, and fails on any command the list does not name.
//
// The scan does not know a receiver's type, so a method of the module's own
// that shares a client method's name reads as that command. Where that
// command is named anyway (a wrapper's Get is a get) that only checks it
// twice; where it is not, the name is listed below with the types that
// define it and its calls are not read, and a new one fails until it is
// looked at and listed.
func TestEveryRedisCommandTheCodeIssuesIsNamed(t *testing.T) {
	methods := goRedisMethodCommands(t)
	if len(methods) < 100 || len(methods["RandomKey"]) == 0 || methods["MemoryUsage"][0] != "memory" {
		t.Fatalf("read %d client methods from go-redis, RandomKey %v, MemoryUsage %v: the reading is broken",
			len(methods), methods["RandomKey"], methods["MemoryUsage"])
	}
	issued := map[string]string{}
	shadowed, called := map[string]bool{}, map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			if err == nil && entry.IsDir() && filepath.ToSlash(path) == "../internal/redistest" {
				// Test support: a Redis the tests start, never a store the
				// process uses.
				return filepath.SkipDir
			}
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		note := func(command string, at token.Pos) {
			command = strings.ToLower(command)
			if _, seen := issued[command]; !seen {
				issued[command] = fset.Position(at).String()
			}
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv != nil && len(methods[function.Name.Name]) > 0 {
				shadowed[function.Name.Name] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !contextArgument(call.Args[0]) {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			case *ast.Ident:
				name = fun.Name
			}
			// A command sent by name: Do(ctx, "hstrlen", ...) or
			// redis.NewStringCmd(ctx, "get", ...).
			if name == "Do" || (strings.HasPrefix(name, "New") && strings.HasSuffix(name, "Cmd")) {
				if len(call.Args) > 1 {
					if literal, ok := call.Args[1].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						if command, err := strconv.Unquote(literal.Value); err == nil {
							note(strings.Fields(command)[0], literal.Pos())
						}
					}
				}
				return true
			}
			if len(methods[name]) > 0 {
				called[name] = true
			}
			if _, own := redisMethodNamesTheModuleDefines[name]; own {
				return true
			}
			for _, command := range methods[name] {
				note(command, call.Pos())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name := range shadowed {
		named := true
		for _, command := range methods[name] {
			named = named && boundedRedisCommand(command) == command
		}
		if _, listed := redisMethodNamesTheModuleDefines[name]; !listed && !named && called[name] {
			t.Errorf("the module defines a method %s, the name of a client method: list it with its types, "+
				"after checking that no call of it reaches Redis by that name", name)
		}
	}
	for name := range redisMethodNamesTheModuleDefines {
		if !shadowed[name] || !called[name] {
			t.Errorf("%s is listed as a method the module defines and no longer is: remove it", name)
		}
	}
	commands := make([]string, 0, len(issued))
	for command := range issued {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, command := range commands {
		if boundedRedisCommand(command) != command {
			t.Errorf("redis command %q, issued at %s, is not in redisCommandNames: redis_command_total counts it as other",
				command, issued[command])
		}
	}
	if testing.Verbose() {
		t.Logf("%d commands issued: %s", len(commands), strings.Join(commands, " "))
	}
}

// redisMethodNamesTheModuleDefines are the client method names a type of
// the module also defines. No call of these names in the module is a Redis
// command of that name today: each is one of these types' own methods.
var redisMethodNamesTheModuleDefines = map[string]string{
	"Publish": "viewstream.Server, controlplane.SnapshotPublisher, fleet.CostProjectionStore, fleet.RedisStore",
	"Wait":    "worker.KeyReservation",
	"Watch":   "openalerts.RedisSubscriber, cmd/alarmd linkdLocationSwitch",
}

// contextArgument says an argument is a context, as far as its spelling
// says: every client method takes one first.
func contextArgument(argument ast.Expr) bool {
	text := types.ExprString(argument)
	return strings.Contains(text, "ctx") || strings.Contains(text, "Ctx") || strings.HasPrefix(text, "context.")
}

// goRedisMethodCommands is, for each command method of the go-redis client
// the module builds against, the commands its body sends: the name given to a
// New*Cmd, the first word of an argument list it builds, or the word it puts
// first in one.
func goRedisMethodCommands(t *testing.T) map[string][]string {
	t.Helper()
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/go-redis/redis/v8").Output()
	if err != nil {
		t.Fatalf("locate go-redis: %v", err)
	}
	dir := strings.TrimSpace(string(output))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	methods := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil || !function.Name.IsExported() {
				continue
			}
			// The command methods: every client and pipeline has these,
			// and a transaction adds WATCH. A Sentinel or PubSub client's
			// methods share names with them and send other commands.
			switch receiver := types.ExprString(function.Recv.List[0].Type); receiver {
			case "cmdable", "statefulCmdable", "*Tx":
			default:
				continue
			}
			seen := map[string]bool{}
			add := func(expression ast.Expr) {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return
				}
				command, err := strconv.Unquote(literal.Value)
				if err != nil || command == "" || seen[strings.ToLower(command)] {
					return
				}
				seen[strings.ToLower(command)] = true
				methods[function.Name.Name] = append(methods[function.Name.Name], strings.ToLower(command))
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CallExpr:
					if ident, ok := typed.Fun.(*ast.Ident); ok && strings.HasPrefix(ident.Name, "New") &&
						strings.HasSuffix(ident.Name, "Cmd") && len(typed.Args) > 1 {
						add(typed.Args[1])
					}
				case *ast.CompositeLit:
					if array, ok := typed.Type.(*ast.ArrayType); ok && len(typed.Elts) > 0 {
						if _, isInterface := array.Elt.(*ast.InterfaceType); isInterface {
							add(typed.Elts[0])
						}
					}
				case *ast.AssignStmt:
					if index, ok := typed.Lhs[0].(*ast.IndexExpr); ok && len(typed.Rhs) == 1 {
						if position, ok := index.Index.(*ast.BasicLit); ok && position.Value == "0" {
							add(typed.Rhs[0])
						}
					}
				}
				return true
			})
		}
	}
	return methods
}
