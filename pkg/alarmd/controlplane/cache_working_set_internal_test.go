// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A cache's working set is what it holds and an entry at its largest charge
// for each one a reader has announced, within its ceiling; before it has
// stored anything an announced entry is taken at the ceiling. An entry is
// struck as it is stored, and what a reader never stores is struck when it
// settles.
func TestACachesWorkingSetIsWhatItHoldsAndWhatItIsAboutToStore(t *testing.T) {
	cache := newObjectReadCache(100, 10_000)
	repository := &RedisCatalogRepository{}
	repository.objectCache.Store(cache)
	size := func() int {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		return cache.working.sizeLocked(cache.bytes, cache.maxBytes)
	}
	if got := size(); got != 0 {
		t.Fatalf("an empty cache nobody reads into = %d, want 0", got)
	}
	reading := cache.announce(2)
	if got := size(); got != 10_000 {
		t.Fatalf("two announced before any entry was seen = %d, want the ceiling", got)
	}
	cache.store(reading, "a", "value", 300)
	if got := size(); got != 600 {
		t.Fatalf("one stored at 300, one to come = %d, want 300 held + 300", got)
	}
	if want := uint64(decodedObjectBytes(600)); func() uint64 { size, _ := repository.ObjectCacheBudget(); return size }() != want {
		t.Fatalf("the object cache's budget is not its working set charged decoded, want %d", want)
	}
	cache.store(reading, "b", "value", 100)
	if got := size(); got != 400 {
		t.Fatalf("both stored = %d, want what it holds, 400", got)
	}
	reading.settle()
	if got := size(); got != 400 {
		t.Fatalf("settled after storing everything = %d, want 400", got)
	}

	// The largest entry is what one to come is taken at, up to the ceiling.
	many := cache.announce(50)
	if got := size(); got != 10_000 {
		t.Fatalf("fifty at 300 over 400 held = %d, want the ceiling", got)
	}
	few := cache.announce(0)
	few.settle()
	many.settle()
	if got := size(); got != 400 {
		t.Fatalf("a reader that stored nothing and settled = %d, want 400", got)
	}

	// An entry already there is struck all the same: the reader is done
	// with it.
	again := cache.announce(1)
	cache.store(again, "a", "value", 300)
	if got := size(); got != 400 {
		t.Fatalf("an entry stored twice = %d, want 400", got)
	}

	// A reading announced to a cache that was replaced since is struck from
	// its own cache when it settles, and never from the new one.
	stale := cache.announce(1)
	replaced := newObjectReadCache(100, 10_000)
	pending := replaced.announce(1)
	replaced.store(stale, "c", "value", 200)
	replaced.mu.Lock()
	if replaced.working.reading != 1 {
		t.Fatalf("the new cache counts %d readings, want its own one still to come", replaced.working.reading)
	}
	replaced.mu.Unlock()
	stale.settle()
	pending.settle()
	if got := size(); got != 400 {
		t.Fatalf("after the replaced cache's reader settled = %d, want 400", got)
	}
}

// The timeline cache strikes an announced timeline as it stores it, under
// whatever version it enters: a batch's stored timelines are held, not also
// still to come.
func TestATimelineIsStruckFromItsAnnouncementAsItIsStored(t *testing.T) {
	cache := newControlReadCache(100, 1<<20)
	reading := cache.announceTimelines(3)
	cache.storeTimeline(reading, "v1", "qg-a", cachedTimelineFor("qg-a", 1), 400)
	charge := uint64(cachedTimelineBytes(400))
	if size, held := cache.timelineBudget(); held != charge || size != held+2*charge {
		t.Fatalf("one of three stored = (%d, %d), want %d held and two more at its charge", size, held, charge)
	}
	cache.storeTimeline(reading, "v2", "qg-b", cachedTimelineFor("qg-b", 2), 400)
	if size, held := cache.timelineBudget(); held != charge || size != held+charge {
		t.Fatalf("the second stored under a new version = (%d, %d), want the first dropped and one still to come", size, held)
	}
	reading.settle()
	if size, held := cache.timelineBudget(); size != held {
		t.Fatalf("settled = (%d, %d), want what it holds", size, held)
	}
}

// workingSetProbe reads a budget at every key a test's Redis is asked for
// under prefix: a store path announces before it reads, so every such read
// sees the budget's size above what it holds.
type workingSetProbe struct {
	budget func() (uint64, uint64)
	prefix string
	reads  int
	unseen []string
}

func (probe *workingSetProbe) onGet(key string) {
	if !strings.HasPrefix(key, probe.prefix) {
		return
	}
	probe.reads++
	if size, held := probe.budget(); size <= held {
		probe.unseen = append(probe.unseen, key)
	}
}

// check runs read against the probe and then asks that it read at least
// once, announced at every read, and settled after, on success and on
// failure alike.
func (probe *workingSetProbe) check(t *testing.T, client *scopeRedis, read func() error) {
	t.Helper()
	client.onGet = probe.onGet
	defer func() { client.onGet = nil }()
	if err := read(); err != nil {
		t.Fatal(err)
	}
	if probe.reads == 0 || len(probe.unseen) != 0 {
		t.Fatalf("%d reads under %s, not announced before %v", probe.reads, probe.prefix, probe.unseen)
	}
	if size, held := probe.budget(); size != held {
		t.Fatalf("after the reads the budget's size is %d over %d held, want what it holds", size, held)
	}
}

// failed runs read against a Redis that fails every read, and asks that
// the reader settled what it announced.
func (probe *workingSetProbe) failed(t *testing.T, client *scopeRedis, read func() error) {
	t.Helper()
	client.getError = errors.New("redis down")
	defer func() { client.getError = nil }()
	if err := read(); err == nil {
		t.Fatal("a read from a Redis that fails every read succeeded")
	}
	if size, held := probe.budget(); size != held {
		t.Fatalf("after a failed read the budget's size is %d over %d held, want the announcement settled", size, held)
	}
}

// Each path that reads a timeline to store it announces the timeline before
// the read and settles after it, the read failing too.
func TestEveryTimelineStorePathAnnouncesBeforeItReads(t *testing.T) {
	groups := []execution.QueryGroupIdentity{"qg-a", "qg-b", "qg-c"}
	version := controlVersion{header: "header-1", known: true}
	ctx := context.Background()
	paths := []struct {
		name string
		read func(*RedisCatalogRepository) error
	}{
		{"loadScheduleTimelineAt", func(repository *RedisCatalogRepository) error {
			for _, group := range groups {
				if _, err := repository.loadScheduleTimelineAt(ctx, group, version); err != nil {
					return err
				}
			}
			return nil
		}},
		{"loadScheduleTimelineAtRevision", func(repository *RedisCatalogRepository) error {
			for _, group := range groups {
				timeline, err := decodeScheduleTimeline(group, []byte(repository.client.(*scopeRedis).values[repository.scheduleTimelineKey(group)]))
				if err != nil {
					return err
				}
				if _, err := repository.loadScheduleTimelineAtRevision(ctx, group, timeline.RecordRevision); err != nil {
					return err
				}
			}
			return nil
		}},
		{"readOpenSegments", func(repository *RedisCatalogRepository) error {
			visited := 0
			err := repository.readOpenSegments(ctx, groups, version, func(execution.QueryGroupIdentity, persistedScheduleSegment) error {
				visited++
				return nil
			})
			if err == nil && visited != len(groups) {
				return errors.New("an open Segment went unread")
			}
			return err
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			repository, client := timelineCacheFixture(t, groups, 4)
			probe := &workingSetProbe{budget: repository.TimelineCacheBudget, prefix: repository.scheduleTimelineKey("")}
			probe.check(t, client, func() error { return path.read(repository) })
			if held := func() uint64 { _, held := repository.TimelineCacheBudget(); return held }(); held == 0 {
				t.Fatal("nothing was stored: the check compared an empty cache")
			}
			failing, client := timelineCacheFixture(t, groups, 4)
			probe = &workingSetProbe{budget: failing.TimelineCacheBudget, prefix: failing.scheduleTimelineKey("")}
			probe.failed(t, client, func() error { return path.read(failing) })
		})
	}
}

// objectCacheFixture serves a small publication's Query Group objects and
// output contexts out of a map.
func objectCacheFixture(t *testing.T) (*RedisCatalogRepository, *scopeRedis, objectCatalogContent) {
	t.Helper()
	content, err := buildObjectCatalogContent(Catalog{QueryGroups: []QueryGroup{
		{Identity: "qg-a", Plans: []FrozenPlan{{Plan: contract.EvaluationPlanV2{PlanID: "1"}}, {Plan: contract.EvaluationPlanV2{PlanID: "2"}}}},
		{Identity: "qg-b", Plans: []FrozenPlan{{Plan: contract.EvaluationPlanV2{PlanID: "3"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	client := &scopeRedis{values: map[string]string{}}
	repository, err := NewRedisCatalogRepository(client, "alarmd:control:working-set", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	for digest, payload := range content.objects {
		client.values[repository.queryGroupObjectKey(digest)] = string(payload)
	}
	for digest, payload := range content.contexts {
		client.values[repository.outputContextKey(digest)] = string(payload)
	}
	if len(content.objects) != 2 || len(content.contexts) == 0 {
		t.Fatalf("fixture holds %d objects and %d contexts, want both kinds", len(content.objects), len(content.contexts))
	}
	return repository, client, content
}

// Each path that reads a catalog object to store it announces the objects
// before the read and settles after it, the read failing too.
func TestEveryObjectStorePathAnnouncesBeforeItReads(t *testing.T) {
	ctx := context.Background()
	paths := []struct {
		name   string
		prefix func(*RedisCatalogRepository) string
		read   func(*RedisCatalogRepository, objectCatalogContent) error
	}{
		{"loadObject", func(repository *RedisCatalogRepository) string { return repository.queryGroupObjectKey("") },
			func(repository *RedisCatalogRepository, content objectCatalogContent) error {
				for _, entry := range content.manifest.QueryGroups {
					if _, _, err := repository.loadStoredQueryGroupObject(ctx, entry.ObjectDigest); err != nil {
						return err
					}
				}
				return nil
			}},
		{"loadQueryGroupObjects", func(repository *RedisCatalogRepository) string { return repository.queryGroupObjectKey("") },
			func(repository *RedisCatalogRepository, content objectCatalogContent) error {
				_, err := repository.loadQueryGroupObjects(ctx, content.manifest.QueryGroups)
				return err
			}},
		{"loadOutputContexts", func(repository *RedisCatalogRepository) string { return repository.outputContextKey("") },
			func(repository *RedisCatalogRepository, content objectCatalogContent) error {
				refs := make([]execution.OutputContextRef, 0, len(content.contexts))
				for digest := range content.contexts {
					refs = append(refs, execution.OutputContextRef{Digest: digest})
				}
				_, err := repository.loadOutputContexts(ctx, refs)
				return err
			}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			repository, client, content := objectCacheFixture(t)
			probe := &workingSetProbe{budget: repository.ObjectCacheBudget, prefix: path.prefix(repository)}
			probe.check(t, client, func() error { return path.read(repository, content) })
			if held := func() uint64 { _, held := repository.ObjectCacheBudget(); return held }(); held == 0 {
				t.Fatal("nothing was stored: the check compared an empty cache")
			}
			failing, client, content := objectCacheFixture(t)
			probe = &workingSetProbe{budget: failing.ObjectCacheBudget, prefix: path.prefix(failing)}
			probe.failed(t, client, func() error { return path.read(failing, content) })
		})
	}
}

// storePathsTested is every function of this package that stores into the
// timeline or the object cache, each one tested above to announce before it
// reads; storeTimelineAtCurrentVersion only passes its caller's reading on.
var storePathsTested = []string{
	"loadObject", "loadOutputContexts", "loadQueryGroupObjects", "loadScheduleTimelineAt",
	"loadScheduleTimelineAtRevision", "readOpenSegments", "storeTimelineAtCurrentVersion",
}

// A store into either cache is made only where the tests above look: a new
// one fails here until it announces its reads and is tested to. None passes
// a nil reading, which would store bytes the working set never counted.
func TestEveryCacheStoreIsOnATestedPath(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The cache stores by their argument counts: the package's other caches
	// have methods named store too.
	arguments := map[string]int{"store": 4, "storeTimeline": 5, "storeTimelineAtCurrentVersion": 4}
	found := map[string]struct{}{}
	fileSet := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if want, named := arguments[selector.Sel.Name]; !named || want != len(call.Args) {
					return true
				}
				found[function.Name.Name] = struct{}{}
				if identifier, ok := call.Args[0].(*ast.Ident); ok && identifier.Name == "nil" {
					t.Errorf("%s at %s stores with no reading", function.Name.Name, fileSet.Position(call.Pos()))
				}
				return true
			})
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(storePathsTested, ",") {
		t.Fatalf("functions storing into the caches = %v, want the tested paths %v", names, storePathsTested)
	}
}
