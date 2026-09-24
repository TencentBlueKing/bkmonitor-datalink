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
	"errors"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// catalogSplitCensusSource answers the split dry run's two reads over the
// catalog and the state store.
//
// The Plans of a Query Group come from its published object, which is the only
// place a Plan's state generation is written down - and the census is keyed by
// that generation, so there is no reading it without one. That object is read
// once per publication per Query Group rather than once per round: a
// publication's content does not change, and the dry run looks at the same
// handful of objects every few seconds for as long as they stay over their
// share. Read every round it would be a steady outbound cost proportional to
// the size of the heaviest objects in the fleet, which is the wrong thing to
// spend on a reading nobody is acting on yet.
type catalogSplitCensusSource struct {
	source   viewSource
	objects  contentQueryGroupLoader
	censuses planCensusReader
	mu       sync.Mutex
	revision string
	planMemo map[execution.QueryGroupIdentity][]splitCandidatePlan
}

// contentQueryGroupLoader is the catalog's object read, narrowed to what this
// needs.
type contentQueryGroupLoader interface {
	LoadContentQueryGroups(
		context.Context, controlplane.PublishedContent, []execution.QueryGroupIdentity,
	) (map[execution.QueryGroupIdentity]controlplane.QueryGroup, error)
}

// planCensusReader is the state store's census read, narrowed the same way.
type planCensusReader interface {
	ReadCensus(context.Context, execution.PlanCensusIdentity) (execution.DimensionCensus, bool, error)
}

func newCatalogSplitCensusSource(
	source viewSource, objects contentQueryGroupLoader, censuses planCensusReader,
) *catalogSplitCensusSource {
	return &catalogSplitCensusSource{source: source, objects: objects, censuses: censuses}
}

// SplitCandidatePlans is the Plans one Query Group carries, each with the
// state generation its census is keyed by.
//
// A Plan whose object carries no generation is left out rather than asked
// about under an empty one: an empty generation is not a generation, and the
// key it would build belongs to nothing.
func (source *catalogSplitCensusSource) SplitCandidatePlans(
	ctx context.Context, queryGroup execution.QueryGroupIdentity,
) ([]splitCandidatePlan, error) {
	if source == nil || source.source == nil || source.objects == nil {
		return nil, errors.New("alarmd: split census source is not configured")
	}
	state, err := source.source.LoadActivationHead(ctx)
	if err != nil {
		return nil, err
	}
	revision := string(state.Current.SnapshotRevision)
	if state.CutoverProgress != nil {
		// While a cutover is in progress a Query Group's Plans are the ones
		// its open Segment runs, not necessarily the manifest's; the memo
		// keeps them apart from the finished publication's.
		revision += "|in-progress"
	}
	if plans, ok := source.memoized(revision, queryGroup); ok {
		return plans, nil
	}
	content, err := source.source.LoadPublishedContent(ctx, state.Current)
	if err != nil {
		return nil, err
	}
	if content.Groups, err = source.source.ApplyCutoverProgress(ctx, state, content.Groups); err != nil {
		return nil, err
	}
	if _, published := content.Groups[queryGroup]; !published {
		// Not in this publication: draining, or gone. Either way it has no
		// Plans to plan a split of, and it is not an error.
		source.remember(revision, queryGroup, nil)
		return nil, nil
	}
	groups, err := source.objects.LoadContentQueryGroups(ctx, content, []execution.QueryGroupIdentity{queryGroup})
	if err != nil {
		return nil, err
	}
	group, ok := groups[queryGroup]
	if !ok {
		source.remember(revision, queryGroup, nil)
		return nil, nil
	}
	plans := make([]splitCandidatePlan, 0, len(group.Plans))
	for _, plan := range group.Plans {
		if plan.StateGeneration == "" {
			continue
		}
		plans = append(plans, splitCandidatePlan{
			Census: execution.PlanCensusIdentity{
				Plan: plan.Identity, StateGeneration: plan.StateGeneration},
			EvaluationIntervalSeconds: plan.ScheduleSpec.EvaluationIntervalSeconds,
			Queries:                   plan.QueryPlans,
		})
	}
	source.remember(revision, queryGroup, plans)
	return plans, nil
}

func (source *catalogSplitCensusSource) ReadCensus(
	ctx context.Context, identity execution.PlanCensusIdentity,
) (execution.DimensionCensus, bool, error) {
	if source == nil || source.censuses == nil {
		return execution.DimensionCensus{}, false, errors.New("alarmd: split census source has no store")
	}
	return source.censuses.ReadCensus(ctx, identity)
}

// memoized is what was read for this publication, and nothing from any other:
// the memo is dropped whole when the publication changes rather than aged
// out, because a Plan's generation belongs to the publication it was compiled
// in and carrying one across is how a census would be read under a key from a
// different build.
func (source *catalogSplitCensusSource) memoized(
	revision string, queryGroup execution.QueryGroupIdentity,
) ([]splitCandidatePlan, bool) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.revision != revision || source.planMemo == nil {
		return nil, false
	}
	plans, ok := source.planMemo[queryGroup]
	return plans, ok
}

func (source *catalogSplitCensusSource) remember(
	revision string, queryGroup execution.QueryGroupIdentity, plans []splitCandidatePlan,
) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.revision != revision || source.planMemo == nil {
		source.revision, source.planMemo = revision, make(map[execution.QueryGroupIdentity][]splitCandidatePlan)
	}
	source.planMemo[queryGroup] = plans
}
