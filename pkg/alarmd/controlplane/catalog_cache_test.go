// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func cacheTestStrategies(t *testing.T) []controlplane.SourceStrategy {
	t.Helper()
	documents := realThresholdDocuments(t)
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	return []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documents[0], Identity: identity},
		{SourceID: "1002", Document: documents[1], Identity: identity},
	}
}

func sameCatalog(left, right controlplane.Catalog) bool {
	return left.SnapshotRevision == right.SnapshotRevision && left.ObservationID == right.ObservationID &&
		reflect.DeepEqual(left.QueryGroups, right.QueryGroups) && reflect.DeepEqual(left.Dispositions, right.Dispositions)
}

// A round whose documents did not change since the previous round compiles
// nothing and builds the same Catalog. The cache-less build is the reference:
// what the cache hands back has to be what the compiler would have produced,
// down to the revision every consumer keys on.
func TestCandidateCacheBuildsTheSameCatalogWithoutCompilingAgain(t *testing.T) {
	ctx := context.Background()
	strategies := cacheTestStrategies(t)
	reference, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	planner := &recordingPlanner{facts: queryFacts(t)}
	cache := controlplane.NewCandidateCache()
	first, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 || planner.calls != 2 {
		t.Fatalf("first round compiled=%d reused=%d planner calls=%d, want every document compiled once", compiled, reused, planner.calls)
	}
	second, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 0 || reused != 2 || planner.calls != 2 {
		t.Fatalf("second round compiled=%d reused=%d planner calls=%d, want nothing compiled", compiled, reused, planner.calls)
	}
	if !sameCatalog(first, reference) || !sameCatalog(second, reference) {
		t.Fatalf("a cached build differs from the reference build: first=%v second=%v", sameCatalog(first, reference), sameCatalog(second, reference))
	}
}

// Only the document that changed goes through the compiler again, and the
// Catalog it builds is the one a cache-less build of the same documents
// produces. The stale compilation does not linger: the cache holds the
// active documents and nothing else.
func TestCandidateCacheRecompilesOnlyTheDocumentThatChanged(t *testing.T) {
	ctx := context.Background()
	strategies := cacheTestStrategies(t)
	planner := &recordingPlanner{facts: queryFacts(t)}
	cache := controlplane.NewCandidateCache()
	before, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]controlplane.SourceStrategy(nil), strategies...)
	changed[1].Document = bytes.Replace(changed[1].Document, []byte(`"threshold":90`), []byte(`"threshold":95`), 1)
	if bytes.Equal(changed[1].Document, strategies[1].Document) {
		t.Fatal("the edit changed nothing; the fixture no longer carries the threshold this test moves")
	}
	after, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: changed, Planner: planner, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 1 || reused != 1 || planner.calls != 3 {
		t.Fatalf("after the edit compiled=%d reused=%d planner calls=%d, want exactly the changed document compiled", compiled, reused, planner.calls)
	}
	if after.SnapshotRevision == before.SnapshotRevision {
		t.Fatal("a changed threshold must change the Catalog revision")
	}
	reference, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: changed, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if !sameCatalog(after, reference) {
		t.Fatal("the partly cached build differs from the reference build of the same documents")
	}
	if cache.Len() != 2 {
		t.Fatalf("cache holds %d compilations, want the two active documents and not the stale one", cache.Len())
	}
}

// A document the compiler rejects is rejected the same way every round, so
// its rejection is remembered too and the dispositions the Catalog carries
// for it do not depend on whether the round compiled it. A document that
// leaves the active set leaves the cache with it.
func TestCandidateCacheForgetsWhatLeftAndRemembersWhatWasRejected(t *testing.T) {
	ctx := context.Background()
	strategies := cacheTestStrategies(t)
	// The business in the identity disagrees with the document, which the
	// compiler refuses before it looks at the query.
	strategies[1].Identity.BusinessID = "3"
	planner := &recordingPlanner{facts: queryFacts(t)}
	cache := controlplane.NewCandidateCache()
	first, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 {
		t.Fatalf("first round compiled=%d reused=%d, want the rejected document counted as compiled", compiled, reused)
	}
	second, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 0 || reused != 2 {
		t.Fatalf("second round compiled=%d reused=%d, want the rejection reused as well", compiled, reused)
	}
	if !sameCatalog(first, second) {
		t.Fatal("a round that reuses a rejection must carry the same dispositions as the round that produced it")
	}
	rejected := false
	for _, disposition := range second.Dispositions {
		if disposition.SourceID == "1002" && disposition.Disposition == controlplane.DispositionConfigRejected {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("the rejected document must still be reported: %+v", second.Dispositions)
	}
	if _, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies[:1], Planner: planner, Cache: cache}); err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 0 || reused != 1 || cache.Len() != 1 {
		t.Fatalf("after 1002 left: compiled=%d reused=%d held=%d, want only 1001 reused and held", compiled, reused, cache.Len())
	}
}

// The wire protocol is part of what the compiler closes over, so a cache that
// compiled under one protocol hands nothing back under another.
func TestCandidateCacheDoesNotServeAnotherProtocol(t *testing.T) {
	ctx := context.Background()
	strategies := cacheTestStrategies(t)
	planner := &recordingPlanner{facts: queryFacts(t)}
	cache := controlplane.NewCandidateCache()
	if _, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache, OutputProtocol: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: cache, OutputProtocol: "native"}); err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 || planner.calls != 4 {
		t.Fatalf("another protocol compiled=%d reused=%d planner calls=%d, want everything compiled again", compiled, reused, planner.calls)
	}
}
