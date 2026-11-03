// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testwait

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// justification marks a fixed wait that is meant: something must not happen
// within the duration, or the duration is the behaviour under test. It counts
// only in a line comment on the wait's own line and only with a reason after
// it: a marker on the line above carries over to whatever is written under it
// later, and an empty one says nothing.
const justification = "fixed-wait:"

// testwaitPath is this package. A wait bounded by what one of its functions
// returns is bounded by the test's deadline, not by a duration of its own.
const testwaitPath = "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"

// timers are the standard library's waits on a duration: they end when the
// duration does, not when the thing waited for happens.
var timers = map[string]bool{
	"time.Sleep":     true,
	"time.After":     true,
	"time.AfterFunc": true,
	"time.NewTimer":  true,
	"time.NewTicker": true,
	"time.Tick":      true,
}

// contextBounds end a context after a duration or at a time. The parent it
// derives from does not change that: the bound is the second argument.
var contextBounds = map[string]bool{
	"context.WithTimeout":       true,
	"context.WithDeadline":      true,
	"context.WithTimeoutCause":  true,
	"context.WithDeadlineCause": true,
}

// clockReads are how a loop watches the wall clock.
var clockReads = map[string]bool{
	"time.Now":   true,
	"time.Since": true,
	"time.Until": true,
}

// timeComparisons are the methods a time.Time is compared with.
var timeComparisons = map[string]bool{
	"Before":  true,
	"After":   true,
	"Equal":   true,
	"Compare": true,
}

// fixedWait is one wait the guard reports: where it starts and what it is.
type fixedWait struct {
	file string
	line int
	kind string
}

func (w fixedWait) String() string {
	return w.file + ":" + strconv.Itoa(w.line) + ": " + w.kind
}

// fixedWaits returns the waits in file that are bounded by a duration rather
// than by what they wait for and do not say why on their own line. file must
// be parsed with its comments and with the parser's identifier resolution,
// which is how a local variable called time is told from the package; the
// waits are reported under filename.
func fixedWaits(fset *token.FileSet, file *ast.File, filename string) []fixedWait {
	w := &waitWalk{
		fset:      fset,
		filename:  filename,
		imports:   map[string]string{},
		self:      file.Name.Name == "testwait",
		justified: map[int]bool{},
		called:    map[ast.Expr]bool{},
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch name {
		case "_":
		case ".":
			w.dotted = append(w.dotted, path)
		default:
			w.imports[name] = path
		}
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text, line := strings.CutPrefix(comment.Text, "//")
			if !line {
				continue
			}
			reason, marked := strings.CutPrefix(strings.TrimSpace(text), justification)
			if marked && strings.TrimSpace(reason) != "" {
				w.justified[fset.Position(comment.Slash).Line] = true
			}
		}
	}
	var enclosing []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			enclosing = enclosing[:len(enclosing)-1]
			return true
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			w.call(n)
		case *ast.SelectorExpr:
			// A function value handed on (sleep := time.Sleep) waits as
			// surely as the call it is made into later.
			if name := w.qualified(n); !w.called[n] && (timers[name] || contextBounds[name]) {
				w.report(n.Pos(), name+" as a value")
			}
		case *ast.ForStmt:
			w.loop(n.For, enclosing, []ast.Node{n.Init, n.Cond, n.Post}, n.Body)
		case *ast.RangeStmt:
			w.loop(n.For, enclosing, nil, n.Body)
		}
		enclosing = append(enclosing, n)
		return true
	})
	sort.SliceStable(w.found, func(i, j int) bool { return w.found[i].line < w.found[j].line })
	return w.found
}

// waitWalk is the state of fixedWaits over one file.
type waitWalk struct {
	fset     *token.FileSet
	filename string
	// imports maps the name a package goes by in this file to its path, so
	// stdtime.Sleep is time.Sleep and time.Sleep is nothing when "time" is
	// not imported under that name.
	imports map[string]string
	// dotted are the paths imported with a dot, whose functions are called
	// bare.
	dotted []string
	// self is set in package testwait, which calls its own functions bare.
	self bool
	// justified holds the lines carrying a "// fixed-wait: <reason>" comment.
	justified map[int]bool
	// called holds the function expressions already judged as calls, so the
	// selector inside a call is not reported a second time as a value.
	called map[ast.Expr]bool
	found  []fixedWait
}

func (w *waitWalk) report(at token.Pos, kind string) {
	line := w.fset.Position(at).Line
	if !w.justified[line] {
		w.found = append(w.found, fixedWait{file: w.filename, line: line, kind: kind})
	}
}

// qualified names the package-level function expr refers to as path.Name, or
// "" when it refers to none. The parser binds each identifier declared in the
// file to its declaration (Obj); a package name is never bound that way, so a
// bound identifier is a local variable or function, whatever it is called.
func (w *waitWalk) qualified(expr ast.Expr) string {
	switch e := ast.Unparen(expr).(type) {
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok || x.Obj != nil {
			return ""
		}
		if path, ok := w.imports[x.Name]; ok {
			return path + "." + e.Sel.Name
		}
	case *ast.Ident:
		if e.Obj == nil {
			for _, path := range w.dotted {
				name := path + "." + e.Name
				if timers[name] || contextBounds[name] || clockReads[name] || path == testwaitPath {
					return name
				}
			}
		}
		if w.self && ast.IsExported(e.Name) && (e.Obj == nil || e.Obj.Kind == ast.Fun) {
			return testwaitPath + "." + e.Name
		}
	}
	return ""
}

// call reports a timer, and a context bound whose bound is not the test's
// own deadline.
func (w *waitWalk) call(call *ast.CallExpr) {
	fun := ast.Unparen(call.Fun)
	name := w.qualified(fun)
	switch {
	case timers[name]:
		w.called[fun] = true
		w.report(call.Pos(), name)
	case contextBounds[name]:
		w.called[fun] = true
		// context.WithDeadline(ctx, testwait.Deadline(t)) ends with the test
		// by construction. time.Until(testwait.Deadline(t)) does not count:
		// whatever is wrapped around the call is a computation of its own.
		if len(call.Args) > 1 && w.intoTestwait(call.Args[1]) {
			return
		}
		parent := "nothing"
		if len(call.Args) > 0 {
			parent = w.parentKind(call.Args[0])
		}
		w.report(call.Pos(), name+" on "+parent)
	}
}

// intoTestwait reports whether expr is itself a call of a testwait function.
func (w *waitWalk) intoTestwait(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	return ok && strings.HasPrefix(w.qualified(call.Fun), testwaitPath+".")
}

// parentKind says what a context bound derives from, for the report only.
func (w *waitWalk) parentKind(expr ast.Expr) string {
	switch e := ast.Unparen(expr).(type) {
	case *ast.CallExpr:
		name := w.qualified(e.Fun)
		switch {
		case name == "context.Background" || name == "context.TODO":
			return name + "()"
		case strings.HasPrefix(name, testwaitPath+"."):
			return "testwait." + strings.TrimPrefix(name, testwaitPath+".") + "(t)"
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && name == "" && sel.Sel.Name == "Context" && len(e.Args) == 0 {
			return "t.Context()"
		}
		return "a call"
	case *ast.Ident, *ast.SelectorExpr:
		return "a variable"
	}
	return "an expression"
}

// loop reports a loop that waits on the wall clock: one whose init, condition
// or post reads the clock, or whose body tests it in an if or switch. Function
// literals in the body may never run, and a loop nested in the body is judged
// on its own, so neither is looked into. A loop is let be when every time it
// compares the clock with holds the test's deadline: it is then a wait on its
// condition, bounded by the test.
func (w *waitWalk) loop(at token.Pos, enclosing []ast.Node, header []ast.Node, body *ast.BlockStmt) {
	// The variables the loop sets from the clock, in its header or anywhere
	// in its body, straight or through another such variable (now :=
	// time.Now(); elapsed := now.Sub(start)): a condition that reads one
	// reads the clock (expired := time.Since(start) > d; if expired),
	// wherever it was set. A variable set from the clock once carries every
	// value the loop sets it to, from the clock or not, so a value set some
	// other way is not mistaken for the clock's. Found until a pass learns
	// nothing new, since a variable can be set from one set further down.
	assigned := map[*ast.Object][]ast.Expr{}
	note := func(set *ast.Ident, value ast.Expr) {
		if set.Obj != nil {
			assigned[set.Obj] = append(assigned[set.Obj], value)
		}
	}
	collect := func(n ast.Node) {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) != len(n.Rhs) {
				return
			}
			for i, value := range n.Rhs {
				if set, ok := n.Lhs[i].(*ast.Ident); ok {
					note(set, value)
				}
			}
		case *ast.ValueSpec:
			for i, value := range n.Values {
				if i < len(n.Names) {
					note(n.Names[i], value)
				}
			}
		}
	}
	// Init and post set, the condition tests (for init; cond; post).
	for i, part := range header {
		if part != nil && i != 1 {
			inLoop(part, collect)
		}
	}
	inLoop(body, collect)
	clockVars := clockVariables{}
	for learned := true; learned; {
		learned = false
		for object, values := range assigned {
			if len(clockVars[object]) > 0 {
				continue
			}
			for _, value := range values {
				if w.readsClockStraight(value, clockVars) {
					clockVars[object], learned = values, true
					break
				}
			}
		}
	}
	var conditions []ast.Node
	if len(header) > 1 && header[1] != nil {
		conditions = append(conditions, header[1])
	}
	inLoop(body, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.IfStmt:
			conditions = append(conditions, n.Cond)
		case *ast.SwitchStmt:
			if n.Tag != nil {
				conditions = append(conditions, n.Tag)
			}
			for _, clause := range n.Body.List {
				for _, test := range clause.(*ast.CaseClause).List {
					conditions = append(conditions, test)
				}
			}
		}
	})
	var compared []ast.Node
	for _, condition := range conditions {
		compared = append(compared, w.clockComparisons(condition, clockVars)...)
	}
	if len(compared) == 0 {
		return
	}
	var fn ast.Node
	for _, n := range enclosing {
		if _, ok := n.(*ast.FuncDecl); ok {
			fn = n
			break
		}
		if _, ok := n.(*ast.FuncLit); ok {
			fn = n
			break
		}
	}
	if fn != nil && w.allOnTestDeadline(compared, fn, clockVars) {
		return
	}
	w.report(at, "clock loop")
}

// clockVariables are the variables a loop sets from the clock, each with
// every value it is set to there.
type clockVariables map[*ast.Object][]ast.Expr

// value is the one value id is set to in the loop: the first, when every
// value is the same plain clock read or there is only one; nil when it is set
// to different things.
func (vars clockVariables) value(id *ast.Ident) ast.Expr {
	values := vars[id.Obj]
	if len(values) == 0 {
		return nil
	}
	same := types.ExprString(ast.Unparen(values[0]))
	for _, other := range values[1:] {
		if types.ExprString(ast.Unparen(other)) != same {
			return nil
		}
	}
	return values[0]
}

// inLoop visits n and what is under it, leaving out function literals, which
// run apart from the loop, and the loops inside it, which are judged at their
// own for.
func inLoop(n ast.Node, visit func(ast.Node)) {
	ast.Inspect(n, func(child ast.Node) bool {
		switch child.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ForStmt, *ast.RangeStmt:
			if child != n {
				return false
			}
		}
		if child != nil {
			visit(child)
		}
		return true
	})
}

// clockComparisons returns, for each clock read in n outside function
// literals - a clock call, or a variable the loop set from one - the
// comparison it takes part in: the nearest enclosing relational expression
// or time comparison method call, or else the read itself (for a variable,
// the value it was set to, as for a bool set from a comparison).
func (w *waitWalk) clockComparisons(n ast.Node, clockVars clockVariables) []ast.Node {
	var found []ast.Node
	var path []ast.Node
	ast.Inspect(n, func(n ast.Node) bool {
		if n == nil {
			path = path[:len(path)-1]
			return true
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		var read ast.Node
		if call, ok := n.(*ast.CallExpr); ok && clockReads[w.qualified(call.Fun)] {
			read = call
		}
		if id, ok := n.(*ast.Ident); ok && id.Obj != nil && len(clockVars[id.Obj]) > 0 {
			// Its one value, or the variable itself when it is set to
			// different things: a read of the clock either way.
			read = id
			if value := clockVars.value(id); value != nil {
				read = value
			}
		}
		if read != nil {
			comparison := read
			for i := len(path) - 1; i >= 0; i-- {
				if w.isComparison(path[i]) {
					comparison = path[i]
					break
				}
			}
			found = append(found, comparison)
		}
		path = append(path, n)
		return true
	})
	return found
}

// readsClockStraight reports whether expr's value comes from a clock read -
// a clock call or a variable the loop set from one - through arithmetic and
// method calls on it (time.Since(start) - d, now.Sub(start)) rather than
// through a call it is an argument of.
func (w *waitWalk) readsClockStraight(expr ast.Expr, clockVars clockVariables) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return e.Obj != nil && len(clockVars[e.Obj]) > 0
	case *ast.CallExpr:
		if clockReads[w.qualified(e.Fun)] {
			return true
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && w.qualified(sel) == "" {
			return w.readsClockStraight(sel.X, clockVars)
		}
	case *ast.SelectorExpr:
		return w.readsClockStraight(e.X, clockVars)
	case *ast.BinaryExpr:
		return w.readsClockStraight(e.X, clockVars) || w.readsClockStraight(e.Y, clockVars)
	case *ast.UnaryExpr:
		return w.readsClockStraight(e.X, clockVars)
	}
	return false
}

func (w *waitWalk) isComparison(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case token.LSS, token.GTR, token.LEQ, token.GEQ, token.EQL, token.NEQ:
			return true
		}
	case *ast.CallExpr:
		sel, ok := n.Fun.(*ast.SelectorExpr)
		return ok && timeComparisons[sel.Sel.Name] && w.qualified(sel) == ""
	}
	return false
}

// allOnTestDeadline reports whether every comparison is on the test's
// deadline (onTestDeadline).
func (w *waitWalk) allOnTestDeadline(comparisons []ast.Node, fn ast.Node, clockVars clockVariables) bool {
	for _, comparison := range comparisons {
		if !w.onTestDeadline(comparison, fn, clockVars) {
			return false
		}
	}
	return true
}

// onTestDeadline reports whether comparison compares the clock as read with
// the test's deadline as set: time.Now() against a variable of fn holding
// testwait.Deadline (Before, After, Equal or Compare, either way round), or
// time.Until of such a variable against 0. A clock variable stands for the
// value it was set to when every value it is set to in the loop is that one;
// set to more than one (now = time.Now(), then now = now.Add(d)), it stands
// for none, so what a comparison means cannot turn on the order of the
// code. Arithmetic on either side - the deadline moved by a constant, the
// clock pushed forward, a duration other than 0 - makes it a fixed time
// again, and does not count.
func (w *waitWalk) onTestDeadline(comparison ast.Node, fn ast.Node, clockVars clockVariables) bool {
	as := func(expr ast.Expr) ast.Expr {
		expr = ast.Unparen(expr)
		if id, ok := expr.(*ast.Ident); ok && id.Obj != nil && len(clockVars[id.Obj]) > 0 {
			if value := clockVars.value(id); value != nil {
				return ast.Unparen(value)
			}
		}
		return expr
	}
	now := func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		return ok && len(call.Args) == 0 && w.qualified(call.Fun) == "time.Now"
	}
	deadline := func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)
		return ok && w.holdsTestDeadline(id, fn)
	}
	until := func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		return ok && len(call.Args) == 1 && w.qualified(call.Fun) == "time.Until" && deadline(ast.Unparen(call.Args[0]))
	}
	zero := func(expr ast.Expr) bool {
		literal, ok := expr.(*ast.BasicLit)
		return ok && literal.Kind == token.INT && literal.Value == "0"
	}
	switch c := comparison.(type) {
	case *ast.CallExpr:
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || len(c.Args) != 1 || !timeComparisons[sel.Sel.Name] {
			return false
		}
		receiver, argument := as(sel.X), as(c.Args[0])
		return now(receiver) && deadline(argument) || deadline(receiver) && now(argument)
	case *ast.BinaryExpr:
		x, y := as(c.X), as(c.Y)
		return until(x) && zero(y) || zero(x) && until(y)
	}
	return false
}

// holdsTestDeadline reports whether id is a variable declared in fn that is
// only ever set, in fn or the closures in it, straight from a
// testwait.Deadline call, and is never handed out by address to be set
// elsewhere. A parameter is set by the caller, and a variable outside fn by
// anyone, so neither does.
func (w *waitWalk) holdsTestDeadline(id *ast.Ident, fn ast.Node) bool {
	object := id.Obj
	if object == nil || object.Kind != ast.Var {
		return false
	}
	decl, ok := object.Decl.(ast.Node)
	if !ok || decl.Pos() < fn.Pos() || decl.End() > fn.End() {
		return false
	}
	sets, held := 0, true
	settle := func(lhs, rhs []ast.Expr, tok token.Token) {
		for i, target := range lhs {
			if target, ok := target.(*ast.Ident); !ok || target.Obj != object {
				continue
			}
			sets++
			if (tok != token.DEFINE && tok != token.ASSIGN) || len(rhs) != len(lhs) || !w.isDeadlineCall(rhs[i]) {
				held = false
			}
		}
	}
	switch decl := object.Decl.(type) {
	case *ast.AssignStmt:
		// A range variable's declaration is an assignment the parser makes
		// up, from the range expression; it is in no statement of fn.
		settle(decl.Lhs, decl.Rhs, decl.Tok)
	case *ast.ValueSpec:
		if len(decl.Values) > 0 {
			names := make([]ast.Expr, len(decl.Names))
			for i, name := range decl.Names {
				names[i] = name
			}
			settle(names, decl.Values, token.DEFINE)
		}
	default:
		return false
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n != object.Decl {
				settle(n.Lhs, n.Rhs, n.Tok)
			}
		case *ast.UnaryExpr:
			if x, ok := ast.Unparen(n.X).(*ast.Ident); ok && n.Op == token.AND && x.Obj == object {
				held = false
			}
		}
		return true
	})
	return held && sets > 0
}

func (w *waitWalk) isDeadlineCall(expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	return ok && w.qualified(call.Fun) == testwaitPath+".Deadline"
}

// guarded reports whether the guard reads the file at relative, a path from
// the module root: every test file, and the test helpers under internal/,
// which every test that calls them waits as. Production code is not read: it
// waits as its design says, and its bounds are reviewed as such.
func guarded(relative string) bool {
	if !strings.HasSuffix(relative, ".go") {
		return false
	}
	return strings.HasSuffix(relative, "_test.go") || strings.HasPrefix(relative, "internal/")
}

// No test in this module waits a fixed time for something that should
// happen: on a loaded host the time runs out first and the test fails for
// nothing. Such a wait is on the condition (Until, Expired, Context). A fixed
// wait that is meant - nothing must happen within it, or a slow peer is what
// the test simulates - says why on its own line. The test helpers under
// internal/ are held to the same rule, since every test that calls them
// waits as they do.
//
// Out of its reach, and left to review: a loop on a helper that reads the
// clock for it (for !waited(start, d)); a loop capped by a count of rounds,
// whose poll sleep carries its own marker; Timer.Reset; a deadline set on a
// connection (SetDeadline) and the timeouts in a client's options.
func TestNoTestWaitsAFixedTimeWithoutSayingWhy(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the module root: %v", root, err)
	}
	fset := token.NewFileSet()
	var found []string
	scanned := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !guarded(relative) {
			return nil
		}
		scanned++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")
		for _, wait := range fixedWaits(fset, file, relative) {
			found = append(found, wait.String()+": "+strings.TrimSpace(lines[wait.line-1]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("no test files scanned; the walk is not reaching the module")
	}
	if len(found) > 0 {
		t.Fatalf("%d fixed waits without a %q reason on their own line - wait on the condition "+
			"(testwait.Until, Expired, Context, a loop on testwait.Deadline) or say why the duration is enough:\n%s",
			len(found), "// "+justification, strings.Join(found, "\n"))
	}
}
