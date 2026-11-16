// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The Slot keeps what the series of one query share once and per series only
// its Dataset and view. A series' inputs, as the evaluator is handed them,
// are still its own: its own records, and the completion's word on its query
// - PARTIAL with the query's evidence, or UNAVAILABLE with no records and the
// reason its attempts named - exactly as a binding kept whole per series had
// them. Two series of each query, so a shared part handed to the wrong series
// or a series' records handed to the other one shows.
func TestEachSeriesIsHandedItsOwnRecordsAndItsQuerysCompletion(t *testing.T) {
	evidence := &execution.PartialEvidence{Kind: execution.PartialEvidenceOmissionStable, Version: 1,
		EvidenceDigest: strings.Repeat("d", 64), OmissionOnly: true, ReturnedRecordsStable: true}
	failed := execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{
		AttemptNo: 1, Endpoint: "uq", Result: execution.RouteAttemptFailed,
		ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable),
	}}}
	for _, test := range []struct {
		name         string
		completeness execution.Completeness
	}{{"the dependency query PARTIAL", execution.CompletenessPartial}, {"the dependency query UNAVAILABLE", execution.CompletenessUnavailable}} {
		t.Run(test.name, func(t *testing.T) {
			header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
			primary, previous := shareFixtureQueries(t, header)
			other := strings.Repeat("d", 64)
			primaryOther := rebindBatchToSeries(t, batches[primary.index], other)
			primaryOther.Delivery.Digest = strings.Repeat("7", 64)
			previousOther := rebindBatchToSeries(t, batches[previous.index], other)
			streamed := []execution.SeriesExecutionBatch{batches[primary.index], primaryOther, batches[previous.index], previousOther}
			delivered := func(first, second execution.SeriesExecutionBatch) execution.SeriesDelivery {
				t.Helper()
				delivery, err := execution.AccumulateSeriesDelivery(first.Delivery, second.Delivery)
				if err != nil {
					t.Fatal(err)
				}
				return delivery
			}
			dependency := execution.PhysicalQueryCompletion{Ref: previousOther.CompletionRef, PhysicalQuery: previous.query.Digest,
				QueryRevision: previous.query.QueryRevision, Completeness: test.completeness, DataState: execution.DataStateData,
				Delivery: delivered(batches[previous.index], previousOther)}
			if test.completeness == execution.CompletenessPartial {
				dependency.PartialEvidence = evidence
			} else {
				dependency.RouteFacts = failed
			}
			completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{
				{Ref: primaryOther.CompletionRef, PhysicalQuery: primary.query.Digest, QueryRevision: primary.query.QueryRevision,
					Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
					Delivery: delivered(batches[primary.index], primaryOther)},
				dependency,
			}}
			completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)

			ports, evaluator, coordinator := workerG4Coordinator(t)
			ports.gapMissing = true
			ports.executeOverride = streamExecution(header, streamed, completion)
			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if len(evaluator.requests) != 2 {
				t.Fatalf("evaluation requests = %d (error %v), want each of the two series evaluated", len(evaluator.requests), err)
			}
			if result.Usage.DependencySeriesDiscarded != 0 {
				t.Fatalf("dependency series discarded = %d, want none: each has its PRIMARY", result.Usage.DependencySeriesDiscarded)
			}
			own := map[execution.SeriesIdentityDigest]map[execution.InputRole]*execution.Dataset{
				execution.SeriesIdentityDigest(strings.Repeat("c", 64)): {
					execution.InputRolePrimary: batches[primary.index].Dataset, execution.InputRoleAlgorithmDependency: batches[previous.index].Dataset},
				execution.SeriesIdentityDigest(other): {
					execution.InputRolePrimary: primaryOther.Dataset, execution.InputRoleAlgorithmDependency: previousOther.Dataset},
			}
			for _, request := range evaluator.requests {
				for _, input := range request.Inputs {
					datasets, known := own[input.SeriesIdentity]
					if !known {
						t.Fatalf("an evaluation of series %s, which was not streamed", input.SeriesIdentity)
					}
					for _, binding := range input.Inputs {
						switch {
						case binding.Role == execution.InputRolePrimary:
							if binding.Dataset != datasets[execution.InputRolePrimary] || binding.View == nil ||
								binding.Completeness != execution.CompletenessFull || binding.Disposition != execution.AccessAvailable {
								t.Fatalf("series %s PRIMARY input = %+v, want its own FULL records", input.SeriesIdentity, binding)
							}
						case test.completeness == execution.CompletenessPartial:
							if binding.Dataset != datasets[execution.InputRoleAlgorithmDependency] || binding.View == nil ||
								binding.Completeness != execution.CompletenessPartial || binding.Disposition != execution.AccessDegraded ||
								binding.ReasonCode != execution.ReasonCode(contract.ReasonQueryPartial) ||
								binding.PartialEvidence == nil || *binding.PartialEvidence != *evidence {
								t.Fatalf("series %s dependency input = %+v, want its own records, PARTIAL with the query's evidence",
									input.SeriesIdentity, binding)
							}
						default:
							reason, attribution := execution.AttributeUnavailable(failed, execution.ReasonCode(contract.ReasonQueryUnavailable))
							if binding.Dataset != nil || binding.View != nil ||
								binding.Completeness != execution.CompletenessUnavailable || binding.DataState != execution.DataStateUnknown ||
								binding.Disposition != execution.AccessUnavailable || binding.ReasonCode != reason ||
								binding.UnavailableAttribution != attribution || binding.PartialEvidence != nil {
								t.Fatalf("series %s dependency input = %+v, want no records, UNAVAILABLE for %s (%s)",
									input.SeriesIdentity, binding, reason, attribution)
							}
						}
					}
				}
			}
		})
	}
}
