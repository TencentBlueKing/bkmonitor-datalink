// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// hostStreamingProvider streams one series per host and reports the delivery it
// decoded - every host, in scope or not - which is what UQ does. The access
// layer, not the provider, knows what was filtered.
type hostStreamingProvider struct{ hosts []string }

func (provider *hostStreamingProvider) Execute(
	ctx context.Context,
	attempt execution.QueryAttempt,
	sink execution.ProviderSeriesSink,
) (execution.ProviderCompletion, error) {
	ref := execution.ProviderResultRef("provider-result")
	var delivery execution.SeriesDelivery
	for _, host := range provider.hosts {
		fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"` + host + `"`)}}
		dimension, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2", fields)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		sourceTime := int64(attempt.Slot.EvaluationTime) - 60
		recordID, err := contract.DeriveRecordIDV2(dimension, sourceTime)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		records := []contract.CanonicalRecordV2{{RecordID: recordID, SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimension},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`60`)},
			Dimensions: map[string]json.RawMessage{
				"host":               json.RawMessage(`"` + host + `"`),
				"bk_target_ip":       json.RawMessage(`"` + host + `"`),
				"bk_target_cloud_id": json.RawMessage(`0`),
			},
			ReceivedTime: int64(attempt.Slot.EvaluationTime)}}
		digest, err := contract.DeriveCanonicalDigestV2("test-delivery", records)
		if err != nil {
			return execution.ProviderCompletion{}, err
		}
		batch := execution.ProviderSeriesBatch{PhysicalQuery: attempt.Spec.Digest, CompletionRef: ref,
			Dataset: execution.NewDataset(records),
			Delivery: execution.SeriesDelivery{PhysicalQuery: attempt.Spec.Digest,
				QueryRevision: attempt.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: digest}}
		if err := sink.ConsumeProviderSeries(ctx, batch); err != nil {
			return execution.ProviderCompletion{}, err
		}
		if delivery, err = execution.AccumulateSeriesDelivery(delivery, batch.Delivery); err != nil {
			return execution.ProviderCompletion{}, err
		}
	}
	return execution.ProviderCompletion{Ref: ref, PhysicalQuery: attempt.Spec.Digest,
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: delivery,
		RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef},
		Stats:      execution.ProviderStats{Series: uint64(len(provider.hosts)), Records: uint64(len(provider.hosts))}}, nil
}

func hostScopeContract(addresses ...string) *contract.TargetScopeV2 {
	return &contract.TargetScopeV2{Groups: []contract.TargetScopeGroupV2{{
		Conditions: []contract.TargetScopeConditionV2{{
			Field: contract.TargetScopeHost, Method: contract.TargetScopeInclude,
			Keys: contract.CanonicalTargetScopeKeys(addresses),
		}},
	}}}
}

// executeScoped runs one slot of a plan whose target is scope against a query
// returning one series per host, and folds the delivered batches the way the
// worker folds them into stream.delivered.
func executeScoped(
	t *testing.T,
	scope *contract.TargetScopeV2,
	hosts ...string,
) (execution.QueryExecutionCompletion, *recordingConsumer, []execution.SeriesDelivery) {
	t.Helper()
	contractRef, frozen := frozenExecution(t)
	frozen.DuePlans[0].CompiledPlan = compilePlanForStrategy(t, "1001", scope)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)

	source, err := NewSource(staticFrozenPlan{plan: frozen}, &hostStreamingProvider{hosts: hosts},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second, Admission: scopedChain()})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }

	consumer := &recordingConsumer{}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var delivered []execution.SeriesDelivery
	for _, batch := range consumer.batches {
		merged := false
		for index := range delivered {
			if delivered[index].PhysicalQuery != batch.PhysicalQuery {
				continue
			}
			accumulated, accumulateErr := execution.AccumulateSeriesDelivery(delivered[index], batch.Delivery)
			if accumulateErr != nil {
				t.Fatalf("accumulate delivery: %v", accumulateErr)
			}
			delivered[index], merged = accumulated, true
			break
		}
		if !merged {
			delivered = append(delivered, batch.Delivery)
		}
	}
	return completion, consumer, delivered
}

// The defect this covers: filtering a series out of every plan removed it from
// the consumer's delivery accounting but left it counted in the provider
// completion, so the worker's completion contract failed and the whole slot
// degraded - including the plans whose own series were admitted. In production
// this silenced two ProcPort strategies entirely rather than narrowing them.
func TestAScopeThatRejectsSomeSeriesStillConservesTheCompletion(t *testing.T) {
	completion, consumer, delivered := executeScoped(t, hostScopeContract("192.0.2.10|0"), "192.0.2.10", "192.0.2.99")

	if len(consumer.batches) != 1 {
		t.Fatalf("delivered batches = %d, want only the in-scope series", len(consumer.batches))
	}
	if err := completion.Validate(consumer.header, delivered); err != nil {
		t.Fatalf("completion no longer conserves what was delivered: %v", err)
	}
	if len(completion.PhysicalQueries) != 1 {
		t.Fatalf("physical completions = %d", len(completion.PhysicalQueries))
	}
	physical := completion.PhysicalQueries[0]
	if physical.DataState != execution.DataStateData || physical.Delivery != delivered[0] {
		t.Fatalf("completion %+v does not describe the forwarded delivery %+v", physical, delivered[0])
	}
	if physical.Delivery.Series != 1 || physical.Delivery.Records != 1 {
		t.Fatalf("completion still counts the rejected series: %+v", physical.Delivery)
	}
}

// When the target rejects every series the query is empty for every plan it
// feeds, which is the same slot outcome as a query that returned nothing.
func TestAScopeThatRejectsEverySeriesCompletesAsEmpty(t *testing.T) {
	completion, consumer, delivered := executeScoped(t, hostScopeContract("192.0.2.10|0"), "192.0.2.98", "192.0.2.99")

	if len(consumer.batches) != 0 {
		t.Fatalf("out-of-scope series were delivered: %+v", consumer.batches)
	}
	if err := completion.Validate(consumer.header, delivered); err != nil {
		t.Fatalf("empty completion does not validate: %v", err)
	}
	physical := completion.PhysicalQueries[0]
	if physical.DataState != execution.DataStateEmpty || physical.Delivery != (execution.SeriesDelivery{}) {
		t.Fatalf("completion = %+v, want EMPTY with no delivery", physical)
	}
	if physical.Completeness != execution.CompletenessFull {
		t.Fatalf("completeness = %q, want the provider's own fact", physical.Completeness)
	}
}

// A scope that admits everything must leave the provider's own completion
// untouched, so the reconciliation cannot drift from it when nothing is filtered.
func TestAScopeThatAdmitsEverySeriesLeavesTheCompletionUntouched(t *testing.T) {
	completion, consumer, delivered := executeScoped(t,
		hostScopeContract("192.0.2.10|0", "192.0.2.11|0"), "192.0.2.10", "192.0.2.11")

	if len(consumer.batches) != 2 {
		t.Fatalf("delivered batches = %d, want both series", len(consumer.batches))
	}
	if err := completion.Validate(consumer.header, delivered); err != nil {
		t.Fatalf("completion conservation: %v", err)
	}
	if completion.PhysicalQueries[0].Delivery.Series != 2 {
		t.Fatalf("delivery = %+v, want both series counted", completion.PhysicalQueries[0].Delivery)
	}
}
