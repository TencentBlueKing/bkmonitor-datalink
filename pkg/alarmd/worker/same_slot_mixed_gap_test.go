// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

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

func TestSameSlotMixedCommittedAndPartialDependencyKeepsGap(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	retryPartial := false
	completionFor := func(bs []execution.SeriesExecutionBatch) execution.QueryExecutionCompletion {
		c := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
		positions := map[execution.PhysicalQueryDigest]int{}
		for _, b := range bs {
			if i, ok := positions[b.PhysicalQuery]; ok {
				merged, err := execution.AccumulateSeriesDelivery(c.PhysicalQueries[i].Delivery, b.Delivery)
				if err != nil {
					t.Fatal(err)
				}
				c.PhysicalQueries[i].Delivery = merged
			} else {
				positions[b.PhysicalQuery] = len(c.PhysicalQueries)
				c.PhysicalQueries = append(c.PhysicalQueries, execution.PhysicalQueryCompletion{Ref: b.CompletionRef, PhysicalQuery: b.PhysicalQuery, QueryRevision: b.QueryRevision, Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: b.Delivery})
			}
		}
		if retryPartial {
			for i := range c.PhysicalQueries {
				for _, b := range bs {
					if b.PhysicalQuery == c.PhysicalQueries[i].PhysicalQuery && b.Inputs[0].Role == execution.InputRoleAlgorithmDependency {
						c.PhysicalQueries[i].Completeness = execution.CompletenessPartial
						c.PhysicalQueries[i].PartialEvidence = &execution.PartialEvidence{Kind: execution.PartialEvidenceOmissionStable, Version: 1, OmissionOnly: true, ReturnedRecordsStable: true, EvidenceDigest: strings.Repeat("d", 64)}
					}
				}
			}
		}
		c.CompletionBindings = accessShapedCompletionBindings(t, header, c.PhysicalQueries)
		return c
	}
	ports, eval, _ := workerG4Coordinator(t)
	ports.gapMissing = false
	ports.failStage = "gap_after"
	store := &mixedInitialWarmingState{persistedRetryState: &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}, due: header.DuePlans[0]}
	co, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: eval, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore, Hosts: worker.SharedHostBusiness, Events: ports, State: store, Progress: ports, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	ports.executeOverride = streamExecution(header, batches, completionFor(batches))
	first, err := co.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err == nil || first.Completed || !strings.Contains(err.Error(), "gap_after") || len(store.saved) != 1 {
		t.Fatalf("first partial commit result=%+v err=%v stored=%d", first, err, len(store.saved))
	}
	var committed execution.StateMutation
	for _, m := range store.saved {
		committed = m
	}
	if len(committed.Levels) != 1 || committed.Levels[0].HistoryCompleteness != execution.HistoryWarming {
		t.Fatalf("fixture must retain WARMING: %+v", committed.Levels)
	}

	// Seed B's older FULL State independently from the series A evidence.
	sibling := committed
	sibling.Identity.SeriesIdentityDigest = execution.SeriesIdentityDigest(strings.Repeat("b", 64))
	oldContract := header.Contract
	oldContract.Slot.EvaluationTime -= 60
	sibling.ApplyVersion, err = execution.BuildApplyVersion(oldContract, header.DuePlans[0].StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	sibling.Levels = append([]execution.RuntimeLevelStateMutation(nil), committed.Levels...)
	sibling.Points = append([]execution.StateHistoryPoint(nil), committed.Points...)
	sibling.AffectedRecords = append([]execution.RecordAnchor(nil), committed.AffectedRecords...)
	for i := range sibling.Levels {
		sibling.Levels[i].HistoryCompleteness = execution.HistoryFull
		sibling.Levels[i].GapReasonCode = ""
		sibling.Levels[i].LastProcessedEventTime -= 60
	}
	for i := range sibling.Points {
		sibling.Points[i].RecordID = strings.Repeat("8", 64)
		sibling.Points[i].SourceTime -= 60
	}
	for i := range sibling.AffectedRecords {
		sibling.AffectedRecords[i].RecordID = strings.Repeat("8", 64)
		sibling.AffectedRecords[i].SourceTime -= 60
	}
	sibling.MutationDigest = ""
	sibling, err = execution.BuildStateMutation(sibling)
	if err != nil {
		t.Fatal(err)
	}
	store.saved[sibling.Identity] = sibling

	mixed := append([]execution.SeriesExecutionBatch(nil), batches...)
	for i, b := range batches {
		records := b.Dataset.Records()
		for j := range records {
			records[j].DimensionIdentity.Digest = string(sibling.Identity.SeriesIdentityDigest)
			records[j].RecordID = strings.Repeat(string(rune('6'+i)), 64)
			if b.Inputs[0].Role == execution.InputRolePrimary {
				records[j].Values["value"] = json.RawMessage(`100`)
			}
		}
		b.Dataset = execution.NewDataset(records)
		b.Inputs = append([]execution.NamedInputBinding(nil), b.Inputs...)
		for j := range b.Inputs {
			b.Inputs[j].Dataset = b.Dataset
			b.Inputs[j].View, err = execution.NewDatasetView(b.Dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
		}
		b.Delivery.Digest = strings.Repeat(string(rune('2'+i)), 64)
		mixed = append(mixed, b)
	}
	calls := len(eval.requests)
	ports.gapMissing = false
	retryPartial = true
	gapCalls := len(ports.gapMutations)
	ports.failStage = ""
	ports.executeOverride = streamExecution(header, mixed, completionFor(mixed))
	result, err := co.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed {
		t.Fatalf("mixed retry result=%+v err=%v", result, err)
	}
	if len(eval.requests) != calls+1 {
		t.Errorf("mixed retry evaluated %d series, want only older B", len(eval.requests)-calls)
	}
	for _, request := range eval.requests[calls:] {
		if request.State.Items[0].Identity != sibling.Identity {
			t.Errorf("committed A was evaluated again: %+v", request.State.Items[0].Identity)
		}
	}
	if !reflect.DeepEqual(store.saved[committed.Identity], committed) {
		t.Error("committed A's State changed")
	}
	if ports.lastProgress.Completion.Kind != execution.CompletionUnavailable {
		t.Errorf("persisted WARMING was lost from completion: %s", ports.lastProgress.Completion.Kind)
	}
	writes := ports.gapMutations[gapCalls:]
	if len(writes) != 1 || writes[0].Scopes[0].Kind != execution.GapStrengthen {
		t.Fatalf("partial retry must only strengthen gap, not subsequently recover it: %+v", writes)
	}
}
