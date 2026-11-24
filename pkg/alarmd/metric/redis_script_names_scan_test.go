// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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
	"strings"
	"testing"
)

// shaConstants are the SHA-1 constants the module sends with EvalSha by hand.
// Each is the digest of a registered text; the tests of their package check
// that it reads as the script.
var shaConstants = map[string]string{
	"fencedBatchWriteSHA": "fencedBatchWriteScript", "applyHashDeltaSHA": "applyHashDeltaScript",
	"recordHashFieldSHA": "recordHashFieldScript", "stageHashSegmentSHA": "stageHashSegmentScript",
	"swapStagedHashSHA": "swapStagedHashScript", "discardStagedHashSHA": "discardStagedHashScript",
	"renewIfBelowSHA": "renewIfBelowScript",
}

// scriptParameters are the script arguments that are a function's parameter,
// with the package's call that passes them on: cliauth sends every script
// through one function, so what must be registered is what its callers pass.
var scriptParameters = map[string]string{"cliauth.script": "run"}

type scriptSourceFile struct {
	dir  string
	file *ast.File
}

// scriptSiteProblems reads non-test source for scripts that would read as
// other, or whose time could not be read under one script. A script is
// registered with a literal name, in the package that defines it.
func scriptSiteProblems(fset *token.FileSet, files []scriptSourceFile) []string {
	registered := map[string]map[string]bool{}
	var problems []string
	report := func(at token.Pos, format string) {
		problems = append(problems, fset.Position(at).String()+": "+format)
	}
	for _, source := range files {
		if registered[source.dir] == nil {
			registered[source.dir] = map[string]bool{}
		}
		ast.Inspect(source.file, func(node ast.Node) bool {
			switch declared := node.(type) {
			case *ast.ValueSpec:
				for index, value := range declared.Values {
					if call, ok := value.(*ast.CallExpr); ok && isScriptDefinition(call) && index < len(declared.Names) {
						registered[source.dir][declared.Names[index].Name] = true
					}
				}
			case *ast.CallExpr:
				if selectorText(declared.Fun) == "redisscript.Register" && len(declared.Args) == 2 {
					if identifier, ok := declared.Args[1].(*ast.Ident); ok {
						registered[source.dir][identifier.Name] = true
					}
				}
			}
			return true
		})
	}
	for _, source := range files {
		dir := source.dir
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			text := selectorText(call.Fun)
			switch {
			case text == "redis.NewScript":
				report(call.Pos(), "redis.NewScript: use redisscript.New, or it reads as other")
			case isScriptDefinition(call) || text == "redisscript.Register":
				if filepath.Base(dir) != "redisatomic" && (len(call.Args) == 0 || !isStringLiteral(call.Args[0])) {
					report(call.Pos(), text+": the name must be a literal, or the label set is not what the source says")
				}
			}
			method := selector.Sel.Name
			if method == "Pipelined" {
				problems = append(problems, mixedBatchProblems(fset, call)...)
			}
			if len(call.Args) < 2 {
				return true
			}
			if _, passesOn := passedOnBy(dir, method); passesOn {
				argument, isIdentifier := call.Args[1].(*ast.Ident)
				if !isIdentifier || !registered[dir][argument.Name] {
					report(call.Pos(), method+": "+types.ExprString(call.Args[1])+" is not a registered script")
				}
				return true
			}
			if !contextArgument(call.Args[0]) {
				return true
			}
			switch method {
			case "Eval", "ScriptLoad":
				argument, isIdentifier := call.Args[1].(*ast.Ident)
				if isIdentifier {
					if _, parameter := scriptParameters[filepath.Base(dir)+"."+argument.Name]; parameter || registered[dir][argument.Name] {
						return true
					}
				}
				report(call.Pos(), method+": "+types.ExprString(call.Args[1])+" is not a registered script")
			case "EvalSha":
				argument, isIdentifier := call.Args[1].(*ast.Ident)
				if !isIdentifier || !registered[dir][shaConstants[argument.Name]] {
					report(call.Pos(), "EvalSha: "+types.ExprString(call.Args[1])+" is not the digest of a registered script")
				}
			}
			return true
		})
	}
	sort.Strings(problems)
	return problems
}

func passedOnBy(dir, method string) (string, bool) {
	for parameter, function := range scriptParameters {
		if strings.HasPrefix(parameter, filepath.Base(dir)+".") && function == method {
			return function, true
		}
	}
	return "", false
}

func isScriptDefinition(call *ast.CallExpr) bool {
	text := selectorText(call.Fun)
	return text == "redisscript.New" || text == "redisatomic.New"
}

func isStringLiteral(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING
}

func selectorText(expression ast.Expr) string {
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		if receiver, ok := selector.X.(*ast.Ident); ok {
			return receiver.Name + "." + selector.Sel.Name
		}
	}
	return ""
}

// mixedBatchProblems finds a pipeline carrying more than one script. A batch
// is timed once, under its first script (redis_script_duration_seconds), which
// says nothing of a second. The hook does not care and must not: this is where
// a mixed batch is found, so its time can be looked at when one appears.
func mixedBatchProblems(fset *token.FileSet, pipelined *ast.CallExpr) []string {
	scripts := map[string]bool{}
	for _, argument := range pipelined.Args {
		function, ok := argument.(*ast.FuncLit)
		if !ok {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "Eval", "ScriptLoad":
				scripts[types.ExprString(call.Args[1])] = true
			case "EvalSha":
				name := types.ExprString(call.Args[1])
				if body, known := shaConstants[name]; known {
					name = body
				}
				scripts[name] = true
			}
			return true
		})
	}
	if len(scripts) < 2 {
		return nil
	}
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	return []string{fset.Position(pipelined.Pos()).String() + ": a pipeline of more than one script: " + strings.Join(names, ", ")}
}

func moduleScriptSources(t *testing.T) (*token.FileSet, []scriptSourceFile) {
	t.Helper()
	fset := token.NewFileSet()
	var files []scriptSourceFile
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch filepath.ToSlash(path) {
			case "../internal/redistest", "../redisscript":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		files = append(files, scriptSourceFile{dir: filepath.Dir(path), file: file})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fset, files
}

// redis_script_total names a script only when it is registered. A script
// nobody registered would read as other, which is found late, on a deployment.
func TestEveryScriptTheCodeRunsIsRegistered(t *testing.T) {
	fset, files := moduleScriptSources(t)
	if len(files) < 100 {
		t.Fatalf("read %d files: the reading is broken", len(files))
	}
	for _, problem := range scriptSiteProblems(fset, files) {
		t.Error(problem)
	}
}

// The scan above finding nothing means something only if it can find.
func TestTheScriptScanFindsWhatItIsThereFor(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{"an unregistered constant", `package p
const body = "return 1"
func f() { client.Eval(ctx, body, nil) }`, "Eval: body is not a registered script"},
		{"text passed directly", `package p
func f() { client.Eval(ctx, "return 1", nil) }`, "is not a registered script"},
		{"an unregistered load", `package p
const body = "return 1"
func f() { pipeline.ScriptLoad(ctx, body) }`, "ScriptLoad: body is not a registered script"},
		{"redis.NewScript", `package p
var script = redis.NewScript("return 1")`, "use redisscript.New"},
		{"an unknown digest", `package p
func f() { client.EvalSha(ctx, someSHA, nil) }`, "is not the digest of a registered script"},
		{"a digest whose script is not registered", `package p
func f() { client.EvalSha(ctx, fencedBatchWriteSHA, nil) }`, "is not the digest of a registered script"},
		{"a name that is not a literal", `package p
var name = "x"
var script = redisscript.New(name, "return 1")`, "the name must be a literal"},
		{"a registered name that is not a literal", `package p
var name = "x"
const body = "return 1"
var _ = redisscript.Register(name, body)`, "the name must be a literal"},
		{"a batch of two scripts", `package p
const one = "return 1"
const two = "return 2"
var _ = redisscript.Register("one", one)
var _ = redisscript.Register("two", two)
func f() { client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
	pipeline.Eval(ctx, one, nil)
	pipeline.Eval(ctx, two, nil)
	return nil
}) }`, "a pipeline of more than one script: one, two"},
		{"a function passing an unregistered script on", `package cliauth
const body = "return 1"
func f() { m.run(r.Context(), body, nil) }`, "run: body is not a registered script"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fixture.go", test.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			dir := "fixture"
			if file.Name.Name == "cliauth" {
				dir = "cliauth"
			}
			problems := scriptSiteProblems(fset, []scriptSourceFile{{dir: dir, file: file}})
			if !strings.Contains(strings.Join(problems, "\n"), test.want) {
				t.Fatalf("problems = %v, want one saying %q", problems, test.want)
			}
		})
	}
	for _, test := range []struct{ name, source string }{
		{"a registered constant", `package p
const body = "return 1"
var _ = redisscript.Register("name", body)
func f() { client.Eval(ctx, body, nil) }`},
		{"a defined script", `package p
var script = redisscript.New("name", "return 1")`},
		{"a defined write", `package p
var write = redisatomic.New("name", "return 1")`},
		{"one script in a batch, loaded and by digest", `package p
const fencedBatchWriteScript = "return 1"
var _ = redisscript.Register("name", fencedBatchWriteScript)
func f() { client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
	pipeline.ScriptLoad(ctx, fencedBatchWriteScript)
	pipeline.EvalSha(ctx, fencedBatchWriteSHA, nil)
	pipeline.Eval(ctx, fencedBatchWriteScript, nil)
	return nil
}) }`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fixture.go", test.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if problems := scriptSiteProblems(fset, []scriptSourceFile{{dir: "fixture", file: file}}); len(problems) != 0 {
				t.Fatalf("problems = %v", problems)
			}
		})
	}
}

// measuredReplyTypes are the reply types receivedBytes handles, read from its
// source so that removing a case is what this test sees.
func measuredReplyTypes(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "redis_bytes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	measured := map[string]bool{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "receivedBytes" {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			if clause, ok := node.(*ast.CaseClause); ok {
				for _, expression := range clause.List {
					if star, ok := expression.(*ast.StarExpr); ok {
						measured[strings.TrimPrefix(types.ExprString(star.X), "redis.")] = true
					}
				}
			}
			return true
		})
	}
	if len(measured) < 5 {
		t.Fatalf("read %d reply types from receivedBytes: the reading is broken", len(measured))
	}
	return measured
}

// goRedisMethodReplyTypes is, for each command method of go-redis, the reply
// type it returns.
func goRedisMethodReplyTypes(t *testing.T) map[string]string {
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
	methods := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Type.Results == nil || len(function.Type.Results.List) != 1 {
				continue
			}
			if receiver := types.ExprString(function.Recv.List[0].Type); receiver != "cmdable" && receiver != "statefulCmdable" {
				continue
			}
			if star, ok := function.Type.Results.List[0].Type.(*ast.StarExpr); ok {
				methods[function.Name.Name] = types.ExprString(star.X)
			}
		}
	}
	return methods
}

// A reply type receivedBytes does not handle counts nothing, silently. This
// reads the client methods the module calls for the types they return.
func TestEveryReplyTypeTheCodeReadsIsMeasured(t *testing.T) {
	methods := goRedisMethodReplyTypes(t)
	if methods["Get"] != "StringCmd" || methods["HGetAll"] != "StringStringMapCmd" {
		t.Fatalf("read Get as %q and HGetAll as %q from go-redis: the reading is broken", methods["Get"], methods["HGetAll"])
	}
	measured := measuredReplyTypes(t)
	fset, files := moduleScriptSources(t)
	sites := map[string]string{}
	for _, source := range files {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !contextArgument(call.Args[0]) {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := selector.Sel.Name
			if _, own := redisMethodNamesTheModuleDefines[name]; own {
				return true
			}
			if replyType, known := methods[name]; known && strings.HasSuffix(replyType, "Cmd") {
				if _, seen := sites[replyType]; !seen {
					sites[replyType] = fset.Position(call.Pos()).String() + " " + name
				}
			}
			return true
		})
	}
	if len(sites) < 5 {
		t.Fatalf("found %d reply types in the module: the reading is broken", len(sites))
	}
	if testing.Verbose() {
		for replyType, site := range sites {
			t.Logf("%s first at %s", replyType, site)
		}
	}
	for replyType, site := range sites {
		if !measured[replyType] {
			t.Errorf("%s returns *redis.%s, which receivedBytes does not measure: its replies would count nothing", site, replyType)
		}
	}
}
