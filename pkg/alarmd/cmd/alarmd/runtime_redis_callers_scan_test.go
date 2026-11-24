// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A client the Redis hook counts reaches its jobs through redisForCaller, or its
// calls read as caller="unnamed", which says nothing about whose they are. What
// else may touch one is the plumbing below and nothing more:
var (
	// clientMethodsAllowed answer for the client itself, not for a job.
	clientMethodsAllowed = map[string]bool{"Close": true, "Ping": true, "AddHook": true}
	// clientCalleesAllowed take a client and do not make a job's calls with it
	// as it is: they name it, count its pool, keep it to close, or name every call
	// they make by its context.
	clientCalleesAllowed = map[string]bool{
		"redisForCaller": true, "redisPoolCounts": true, "append": true,
		"censusStoresOf": true, "NewDiagnosticStore": true, "NewCostProjectionStore": true,
		"openPhaseTwoCoreResources": true,
	}
	// clientFieldsAllowed are the fields that keep a client for a job that names
	// every call it makes by its context, by the type and the field.
	clientFieldsAllowed = map[string]bool{"observationCostRefresh.reader": true}
	// clientParameters are the parameters a function above that holds to this
	// rule receives a client in, read as if it had opened them.
	clientParameters = map[string][]string{"openPhaseTwoCoreResources": {"controlClient", "runtimeClient"}}
	// clientOpeners return a client that carries the hook.
	clientOpeners = map[string]bool{"openRedis": true, "openProductionRedisOptionsWithHook": true}
)

// unnamedClientUses reads non-test source for a hooked client used any other way.
func unnamedClientUses(fset *token.FileSet, files []*ast.File) []string {
	var problems []string
	for _, file := range files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || clientOpeners[function.Name.Name] {
				continue
			}
			clients := map[string]bool{}
			for _, name := range clientParameters[function.Name.Name] {
				clients[name] = true
			}
			collectHookedClients(function.Body, clients)
			if len(clients) == 0 {
				continue
			}
			parents := map[ast.Node]ast.Node{}
			var stack []ast.Node
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if node == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if len(stack) > 0 {
					parents[node] = stack[len(stack)-1]
				}
				stack = append(stack, node)
				return true
			})
			ast.Inspect(function.Body, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok || !clients[identifier.Name] || clientUseAllowed(identifier, parents) {
					return true
				}
				problems = append(problems, fset.Position(identifier.Pos()).String()+": "+identifier.Name+
					" is a hooked Redis client handed on without redisForCaller, so its calls read as caller=unnamed")
				return true
			})
		}
	}
	sort.Strings(problems)
	return problems
}

// collectHookedClients adds what the body opens with the hook, and what it then
// calls by another name, until nothing more is added.
func collectHookedClients(body *ast.BlockStmt, clients map[string]bool) {
	// A client made directly counts only once the hook is on it.
	hooked := map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "AddHook" {
				if receiver, ok := selector.X.(*ast.Ident); ok {
					hooked[receiver.Name] = true
				}
			}
		}
		return true
	})
	for changed := true; changed; {
		changed = false
		ast.Inspect(body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Rhs) == 0 {
				return true
			}
			target, ok := assign.Lhs[0].(*ast.Ident)
			if !ok || clients[target.Name] {
				return true
			}
			adds := false
			switch value := assign.Rhs[0].(type) {
			case *ast.Ident:
				adds = clients[value.Name]
			case *ast.CallExpr:
				switch callee := calleeName(value); {
				case clientOpeners[callee]:
					adds = true
				case callee == "NewUniversalClient":
					adds = hooked[target.Name]
				}
			}
			if adds {
				clients[target.Name] = true
				changed = true
			}
			return true
		})
	}
}

func calleeName(call *ast.CallExpr) string {
	switch callee := call.Fun.(type) {
	case *ast.Ident:
		return callee.Name
	case *ast.SelectorExpr:
		return callee.Sel.Name
	}
	return ""
}

func clientUseAllowed(identifier *ast.Ident, parents map[ast.Node]ast.Node) bool {
	switch use := parents[identifier].(type) {
	case *ast.KeyValueExpr:
		literal, isLiteral := parents[use].(*ast.CompositeLit)
		if !isLiteral {
			return false
		}
		kind, isNamed := literal.Type.(*ast.Ident)
		key, isField := use.Key.(*ast.Ident)
		if !isNamed || !isField {
			return false
		}
		return use.Key != identifier && clientFieldsAllowed[kind.Name+"."+key.Name]
	case *ast.SelectorExpr:
		return use.X == identifier && clientMethodsAllowed[use.Sel.Name]
	case *ast.CallExpr:
		return clientCalleesAllowed[calleeName(use)]
	case *ast.AssignStmt:
		// To it, or under another name, which is then read the same.
		for _, value := range use.Rhs {
			if value == identifier {
				return true
			}
		}
		for _, target := range use.Lhs {
			if target == identifier {
				return true
			}
		}
		return false
	case *ast.BinaryExpr, *ast.ValueSpec:
		return true
	}
	return false
}

func parseClientSource(t *testing.T, fset *token.FileSet, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(fset, "source.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// The reading itself, on the two ways the legacy output's clients went unnamed
// and on the way they are named now.
func TestTheClientReadingFindsAnUnnamedClient(t *testing.T) {
	for name, wanted := range map[string]struct {
		source   string
		problems int
	}{
		"in a field": {`package p
func f() { c, _ := w.openRedis(); use(Store{Client: c}) }`, 1},
		"as an argument": {`package p
func f() { c := redis.NewUniversalClient(o); c.AddHook(h); use(c) }`, 1},
		"under another name": {`package p
func f() { c, _ := w.openRedis(); other := c; use(other) }`, 1},
		"named": {`package p
func f() { c, _ := w.openRedis(); use(Store{Client: redisForCaller(c, name)}); _ = c.Close() }`, 0},
		"no hook is no count": {`package p
func f() { c := redis.NewUniversalClient(o); use(c) }`, 0},
		"a field of a job that names its calls": {`package p
func f() { c, _ := w.openRedis(); use(&observationCostRefresh{reader: c}) }`, 0},
		"another field of the same": {`package p
func f() { c, _ := w.openRedis(); use(&observationCostRefresh{other: c}) }`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			if problems := unnamedClientUses(fset, []*ast.File{parseClientSource(t, fset, wanted.source)}); len(problems) != wanted.problems {
				t.Fatalf("problems = %v, want %d", problems, wanted.problems)
			}
		})
	}
}

func TestEveryHookedRedisClientReachesItsJobsNamed(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, parseClientSource(t, fset, string(source)))
	}
	if len(files) == 0 {
		t.Fatal("no source was read")
	}
	if problems := unnamedClientUses(fset, files); len(problems) != 0 {
		t.Fatalf("unnamed Redis clients:\n%s", strings.Join(problems, "\n"))
	}
}

// capturingLegacySink keeps the converter the bundle configures the output with.
type capturingLegacySink struct {
	recordingPhaseTwoEventSink
	converter enginekafka.LegacyEventConverter
}

func (sink *capturingLegacySink) ConfigureLegacyOutput(converter enginekafka.LegacyEventConverter, _ string, _ int) error {
	sink.converter = converter
	return nil
}

// The compatibility output's calls, through the converter the production bundle
// configured, read under their own names, and what is left unnamed is a ping.
func TestTheLegacyOutputsCallsNameTheirCaller(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := controlledG4RuntimeConfig(address, "http://callers-uq", "alarmd-callers")
	cfg.Kafka.LegacyAdapter.PodCache = &config.LegacyPodCacheConfig{
		Connection: config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: address,
			DialTimeout: cfg.Redis.DialTimeout, ReadTimeout: cfg.Redis.ReadTimeout, WriteTimeout: cfg.Redis.WriteTimeout},
		KeyPrefix: "alarmd-callers-pods",
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	sink := &capturingLegacySink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: &http.Client{},
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return sink, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	converter, ok := sink.converter.(*legacyoutput.Converter)
	if !ok || converter == nil {
		t.Fatalf("the bundle configured the output with %T", sink.converter)
	}
	if err := converter.Store.SaveBatch(ctx, []legacyoutput.Snapshot{{StrategyID: 1, Key: "alarmd-callers-snapshot", Value: []byte("{}")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := converter.Pods.LookupPod(ctx, legacyoutput.PodLookup{ClusterID: "c", Name: "p"}); err != nil {
		t.Logf("the Pod lookup answered %v: a miss still asks", err)
	}
	operations, _ := callerCounts(t, recorder)
	for key, want := range map[string]float64{"legacy_output/legacy_output": 1, "legacy_pod_cache/legacy_pod_cache": 1} {
		if operations[key] != want {
			t.Fatalf("operations = %v, want %s = %v", operations, key, want)
		}
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	sawPing := false
	for _, family := range families {
		if family.GetName() != "alarmd_redis_bytes_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range series.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["caller"] != "unnamed" || series.GetCounter().GetValue() == 0 {
				continue
			}
			if labels["command"] != "ping" {
				t.Fatalf("an unnamed call that is not a ping: %v", labels)
			}
			sawPing = true
		}
	}
	if !sawPing {
		t.Fatal("no unnamed ping was read: the reading above would pass on a family that never moved")
	}
}
