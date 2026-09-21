// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The wrapper persists actual evaluator mutations; recordingPorts alone always
// returns missing state and cannot exercise a retry after a partial commit.
type persistedRetryState struct {
	*recordingPorts
	saved map[execution.StateKeyIdentity]execution.StateMutation
}

func (s *persistedRetryState) LoadRuntime(ctx context.Context, req execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	out, err := s.recordingPorts.LoadRuntime(ctx, req)
	if err != nil {
		return out, err
	}
	for i, item := range req.Items {
		m, ok := s.saved[item.Identity]
		if !ok {
			continue
		}
		v := execution.RuntimeStateView{Identity: m.Identity, Status: execution.StateFoundReady, BlobRevision: m.ExpectedBlobRevision + 1,
			PersistedApplyVersion: m.ApplyVersion, PersistedMutationDigest: m.MutationDigest, History: append([]execution.StateHistoryPoint(nil), m.Points...), SeriesGuard: m.SeriesGuard}
		for _, l := range m.Levels {
			v.Levels = append(v.Levels, execution.RuntimeLevelStateView{LevelID: l.LevelID, LevelStateCompatibility: l.LevelStateCompatibility,
				HistoryCompleteness: l.HistoryCompleteness, GapReasonCode: l.GapReasonCode, WarmupRequirementRef: l.WarmupRequirementRef, LastProcessedEventTime: l.LastProcessedEventTime})
			if l.HistoryCompleteness == execution.HistoryWarming {
				v.Status = execution.StateFoundWarming
			}
			if l.HistoryCompleteness == execution.HistoryGapped {
				v.Status = execution.StateFoundGapped
			}
		}
		out.Items[i] = v
	}
	return out, nil
}

func (s *persistedRetryState) ApplyRuntime(ctx context.Context, req execution.StateApplyRequest) (execution.StateApplyResult, error) {
	out, err := s.recordingPorts.ApplyRuntime(ctx, req)
	if err == nil {
		for _, m := range req.Items {
			s.saved[m.Identity] = m
		}
	}
	return out, err
}

func TestAlreadyCommittedSeriesBypassesRealEvaluator(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "same input"
		if changed {
			name = "changed input"
		}
		t.Run(name, func(t *testing.T) {
			header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
			completionFor := func(bs []execution.SeriesExecutionBatch) execution.QueryExecutionCompletion {
				c := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
				for _, b := range bs {
					c.PhysicalQueries = append(c.PhysicalQueries, execution.PhysicalQueryCompletion{Ref: b.CompletionRef, PhysicalQuery: b.PhysicalQuery, QueryRevision: b.QueryRevision, Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: b.Delivery})
				}
				c.CompletionBindings = accessShapedCompletionBindings(t, header, c.PhysicalQueries)
				return c
			}
			ports, eval, _ := workerG4Coordinator(t)
			ports.gapMissing = true
			ports.failStage = "progress_commit"
			state := &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}
			co, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: eval, Admission: ports,
				GapGuard: ports, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness, Events: ports, State: state, Progress: ports,
				Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
			if err != nil {
				t.Fatal(err)
			}
			ports.executeOverride = streamExecution(header, batches, completionFor(batches))
			first, err := co.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err == nil || first.Completed || !strings.Contains(err.Error(), "progress_commit") || len(state.saved) != 1 || len(eval.requests) != 1 {
				t.Fatalf("first attempt must write real State then fail Progress: result=%+v err=%v stored=%d evaluations=%d", first, err, len(state.saved), len(eval.requests))
			}
			prior := map[execution.StateKeyIdentity]execution.StateMutation{}
			for k, v := range state.saved {
				prior[k] = v
			}
			applied, events := ports.stateApplyCalls, ports.eventCount
			if changed {
				for i, b := range batches {
					if b.Inputs[0].Role != execution.InputRolePrimary {
						continue
					}
					records := b.Dataset.Records()
					records[0].Values["value"] = json.RawMessage(`100`)
					b.Dataset = execution.NewDataset(records)
					b.Inputs = append([]execution.NamedInputBinding(nil), b.Inputs...)
					for j := range b.Inputs {
						b.Inputs[j].Dataset = b.Dataset
						b.Inputs[j].View, err = execution.NewDatasetView(b.Dataset, []uint32{0})
						if err != nil {
							t.Fatal(err)
						}
					}
					b.Delivery.Digest = strings.Repeat("9", 64)
					batches[i] = b
				}
			}
			ports.failStage = ""
			ports.executeOverride = streamExecution(header, batches, completionFor(batches))
			second, err := co.Execute(context.Background(), workerSlotRequest(header.Contract))
			if len(eval.requests) != 1 {
				t.Errorf("already committed series was re-evaluated: calls=%d want 1; retry err=%v", len(eval.requests), err)
			}
			if err != nil || !second.Completed {
				t.Errorf("same Slot must finish Progress: result=%+v err=%v", second, err)
			}
			if ports.stateApplyCalls != applied || ports.eventCount != events || !reflect.DeepEqual(prior, state.saved) {
				t.Errorf("retry changed committed side effects: state calls %d->%d events %d->%d", applied, ports.stateApplyCalls, events, ports.eventCount)
			}
		})
	}
}
