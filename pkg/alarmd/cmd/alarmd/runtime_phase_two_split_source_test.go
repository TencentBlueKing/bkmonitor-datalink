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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type fakeSplitViewSource struct {
	revision execution.SnapshotRevision
	groups   map[execution.QueryGroupIdentity]controlplane.ContentEntry
	loads    int
	// running is what the open Segments run while inProgress is set.
	inProgress bool
	running    map[execution.QueryGroupIdentity]controlplane.ContentEntry
}

func (source *fakeSplitViewSource) LoadActivationHead(context.Context) (controlplane.ActivationState, error) {
	state := controlplane.ActivationState{Current: controlplane.SnapshotPublicationRef{SnapshotRevision: source.revision}}
	if source.inProgress {
		state.CutoverProgress = &controlplane.CutoverProgress{}
	}
	return state, nil
}

func (source *fakeSplitViewSource) LoadPublishedContent(
	context.Context, controlplane.SnapshotPublicationRef,
) (controlplane.PublishedContent, error) {
	return controlplane.PublishedContent{Groups: source.groups}, nil
}

func (source *fakeSplitViewSource) ActivationBlocked(context.Context) ([]controlplane.BlockedQueryGroup, error) {
	return nil, nil
}

func (source *fakeSplitViewSource) DrainingContent(
	context.Context, execution.QueryGroupIdentity,
) (execution.ObjectDigest, []execution.OutputContextRef, bool, error) {
	return "", nil, false, nil
}

type fakeContentLoader struct {
	groups  map[execution.QueryGroupIdentity]controlplane.QueryGroup
	loads   int
	digests []execution.ObjectDigest
}

func (loader *fakeContentLoader) LoadContentQueryGroups(
	_ context.Context, content controlplane.PublishedContent, identities []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]controlplane.QueryGroup, error) {
	loader.loads++
	for _, identity := range identities {
		loader.digests = append(loader.digests, content.Groups[identity].Digest)
	}
	result := make(map[execution.QueryGroupIdentity]controlplane.QueryGroup, len(identities))
	for _, identity := range identities {
		if group, ok := loader.groups[identity]; ok {
			result[identity] = group
		}
	}
	return result, nil
}

type fakeCensusReader struct{}

func (fakeCensusReader) ReadCensus(
	context.Context, execution.PlanCensusIdentity,
) (execution.DimensionCensus, bool, error) {
	return execution.DimensionCensus{}, false, nil
}

// A candidate's Plans come back with everything the dry run needs about them:
// the census key, the cadence its staleness is judged by, and the queries the
// split has to be expressed in.
//
// Asserted on what this adapter returns rather than through a fake source,
// because the fakes the dry-run tests use set these fields themselves - so
// dropping the line that copies the queries here left every one of those
// tests green while the production path handed the transform nothing, which
// it answers with "this Plan has no queries".
func TestTheCandidatesPlansCarryEverythingTheDryRunAsksOfThem(t *testing.T) {
	queries := splitDryRunQueries(t, execution.QueryConditions{})
	group := controlplane.QueryGroup{Plans: []controlplane.FrozenPlan{{
		Identity:        execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
		ScheduleSpec:    execution.ScheduleSpec{EvaluationIntervalSeconds: 60},
		QueryPlans:      queries,
	}}}
	view := &fakeSplitViewSource{revision: "snapshot-1",
		groups: map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg": {}}}
	loader := &fakeContentLoader{groups: map[execution.QueryGroupIdentity]controlplane.QueryGroup{"qg": group}}
	source := newCatalogSplitCensusSource(view, loader, fakeCensusReader{})

	plans, err := source.SplitCandidatePlans(context.Background(), "qg")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("%d Plans, want the one the object carries", len(plans))
	}
	if plans[0].Census.StateGeneration != "generation" {
		t.Fatalf("census key = %+v, want the generation the census is keyed by", plans[0].Census)
	}
	if plans[0].EvaluationIntervalSeconds != 60 {
		t.Fatalf("cadence = %d, want the Plan's own: without it the census is judged by a fixed floor",
			plans[0].EvaluationIntervalSeconds)
	}
	if len(plans[0].Queries) != len(queries) {
		t.Fatalf("%d queries carried, want %d: handed none, the transform answers that the Plan has no "+
			"queries, and an object whose split is perfectly expressible reads as one that cannot be cut",
			len(plans[0].Queries), len(queries))
	}
}

// The object is read once per publication, not once per round. A Plan's
// state generation is only written in its published object and the census
// key is built from it, so this read cannot be skipped - but a publication's
// content does not change, and the dry run looks at the same handful of
// objects every few seconds for as long as they stay over their share.
func TestACandidatesObjectIsReadOncePerPublication(t *testing.T) {
	group := controlplane.QueryGroup{Plans: []controlplane.FrozenPlan{{
		Identity:        execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
		ScheduleSpec:    execution.ScheduleSpec{EvaluationIntervalSeconds: 60},
		QueryPlans:      splitDryRunQueries(t, execution.QueryConditions{}),
	}}}
	view := &fakeSplitViewSource{revision: "snapshot-1",
		groups: map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg": {}}}
	loader := &fakeContentLoader{groups: map[execution.QueryGroupIdentity]controlplane.QueryGroup{"qg": group}}
	source := newCatalogSplitCensusSource(view, loader, fakeCensusReader{})

	for round := 0; round < 4; round++ {
		if _, err := source.SplitCandidatePlans(context.Background(), "qg"); err != nil {
			t.Fatal(err)
		}
	}
	if loader.loads != 1 {
		t.Fatalf("the object was read %d times over four rounds, want once: read every round it is a "+
			"standing outbound cost proportional to the largest objects in the fleet, for a reading "+
			"nobody acts on yet", loader.loads)
	}

	// A new publication is a new set of generations, so the memo goes whole.
	view.revision = "snapshot-2"
	if _, err := source.SplitCandidatePlans(context.Background(), "qg"); err != nil {
		t.Fatal(err)
	}
	if loader.loads != 2 {
		t.Fatalf("the object was read %d times across two publications, want once each: a generation "+
			"carried over from another publication builds a census key that belongs to nothing",
			loader.loads)
	}
}

// ApplyCutoverProgress gives what the open Segments run while a cutover is
// in progress, and the content as given otherwise.
func (source *fakeSplitViewSource) ApplyCutoverProgress(
	_ context.Context, state controlplane.ActivationState, content map[execution.QueryGroupIdentity]controlplane.ContentEntry,
) (map[execution.QueryGroupIdentity]controlplane.ContentEntry, error) {
	if state.CutoverProgress == nil {
		return content, nil
	}
	return source.running, nil
}

// While a cutover is in progress a candidate's Plans are read from the
// object its open Segment runs, not the manifest's, and not remembered past
// the cutover: once it finishes, the same revision names the manifest's
// object and that is read.
func TestACandidatesPlansFollowItsOpenSegmentWhileACutoverIsInProgress(t *testing.T) {
	group := controlplane.QueryGroup{Plans: []controlplane.FrozenPlan{{
		Identity:        execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
		ScheduleSpec:    execution.ScheduleSpec{EvaluationIntervalSeconds: 60},
		QueryPlans:      splitDryRunQueries(t, execution.QueryConditions{}),
	}}}
	view := &fakeSplitViewSource{revision: "snapshot-2", inProgress: true,
		groups:  map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg": {Digest: "new"}},
		running: map[execution.QueryGroupIdentity]controlplane.ContentEntry{"qg": {Digest: "old"}}}
	loader := &fakeContentLoader{groups: map[execution.QueryGroupIdentity]controlplane.QueryGroup{"qg": group}}
	source := newCatalogSplitCensusSource(view, loader, fakeCensusReader{})

	if _, err := source.SplitCandidatePlans(context.Background(), "qg"); err != nil {
		t.Fatal(err)
	}
	view.inProgress = false
	if _, err := source.SplitCandidatePlans(context.Background(), "qg"); err != nil {
		t.Fatal(err)
	}
	if len(loader.digests) != 2 || loader.digests[0] != "old" || loader.digests[1] != "new" {
		t.Fatalf("objects read = %v, want the open Segment's while in progress, then the manifest's", loader.digests)
	}
}
