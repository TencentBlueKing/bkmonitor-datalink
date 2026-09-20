// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

type countingHostBusiness struct {
	byIdentity map[string]string
	lookups    int
}

func (lookup *countingHostBusiness) LookupHostBusiness(identity string) (string, bool) {
	lookup.lookups++
	business, held := lookup.byIdentity[identity]
	return business, held
}

// HostIndexResolved follows the map, so a fake standing for a process with no
// index cannot claim to have resolved anything out of it.
func (lookup *countingHostBusiness) HostIndexResolved() bool { return len(lookup.byIdentity) > 0 }

func hostCandidate(t *testing.T, ip, cloud string) nodata.HostCandidate {
	t.Helper()
	return nodata.HostCandidate{
		GroupKey: "group:" + ip,
		Host:     nodata.HostIdentity{IP: ip, CloudID: cloud},
	}
}

// One pass over the index answers both questions, because both come from the
// same fact: the business the host is held under.
//
// The three outcomes are distinct and a host can land in neither set. One the
// index does not hold is not expected and has not left - it was never here,
// which is the backend intersecting a declared target with the business's
// hosts.
func TestResolvingNoDataHostsAnswersBothQuestionsInOnePass(t *testing.T) {
	index := &countingHostBusiness{byIdentity: map[string]string{
		"10.0.0.1|0": "2",
		"10.0.0.2|0": "9",
	}}
	candidates := []nodata.HostCandidate{
		hostCandidate(t, "10.0.0.1", "0"), // this business
		hostCandidate(t, "10.0.0.2", "0"), // another business
		hostCandidate(t, "10.0.0.3", "0"), // not held at all
	}

	resolution := resolveNoDataHosts(index, "2", candidates)

	if len(resolution.Known) != 1 {
		t.Fatalf("known = %+v, want only the host held under this business", resolution.Known)
	}
	if _, ok := resolution.Known["10.0.0.1|0"]; !ok {
		t.Fatalf("known = %+v, want the host of this business", resolution.Known)
	}
	if len(resolution.OutOfBusiness) != 1 {
		t.Fatalf("out of business = %+v, want only the host held elsewhere", resolution.OutOfBusiness)
	}
	// Keyed by group, because that is what the evaluation asks about. Keyed by
	// host it would never match, and the group would stay absent forever.
	if _, ok := resolution.OutOfBusiness[candidates[1].GroupKey]; !ok {
		t.Fatalf("out of business = %+v, want it keyed by group %q",
			resolution.OutOfBusiness, candidates[1].GroupKey)
	}
	if _, ok := resolution.Known["10.0.0.3|0"]; ok {
		t.Fatal("a host the index does not hold was expected")
	}
	if _, ok := resolution.OutOfBusiness[candidates[2].GroupKey]; ok {
		t.Fatal("a host the index does not hold was reported as having left the business")
	}

	// One lookup per candidate: the two answers come from the same read, and a
	// second pass for the out-of-business set would double the per-Slot cost of
	// every static target.
	if index.lookups != len(candidates) {
		t.Fatalf("lookups = %d, want one per candidate (%d)", index.lookups, len(candidates))
	}
}

// rememberingNoDataStore answers one Plan's load with a stored memory.
type rememberingNoDataStore struct {
	groups []execution.NoDataGroupMemory
}

func (store *rememberingNoDataStore) LoadNoData(
	_ context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.NoDataMemorySnapshot{
			Identity: item.Identity, Status: execution.NoDataMemoryFound, MarkerRevision: 1,
			SchemaVersion: execution.NoDataMemorySchemaV1, PersistedApplyVersion: item.ApplyVersion,
			PersistedMutationDigest: "digest", LastScheduleRevision: item.ScheduleRevision,
			RosterVersion: "HISTORY/1", Groups: store.groups,
		}
	}
	return result, nil
}

func (store *rememberingNoDataStore) ApplyNoData(
	_ context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		result.Items[index] = execution.NoDataApplyItemResult{
			Identity: mutation.Identity, Status: execution.NoDataApplied,
		}
	}
	return result, nil
}

// The hosts a history roster consults are the ones the Plan remembers, so they
// cannot be known until the memory has been read - which is why the resolution
// happens after the load and not before it.
func TestNoDataHostsAreResolvedFromTheMemoryJustLoaded(t *testing.T) {
	remembered := nodata.HostIdentity{IP: "10.0.0.9", CloudID: "0"}
	// Built by the same projection the evaluation uses rather than written out
	// here: the key's shape is the package's, and a hand-written one is a guess
	// that happens to agree until it does not.
	rememberedGroup, ok := nodata.Project(
		map[string]string{"bk_target_ip": remembered.IP, "bk_target_cloud_id": remembered.CloudID},
		[]string{"bk_target_ip", "bk_target_cloud_id"},
	)
	if !ok {
		t.Fatal("fixture: the host dimensions did not project to a group")
	}
	rememberedKey := rememberedGroup.Key()
	store := &rememberingNoDataStore{groups: []execution.NoDataGroupMemory{
		{GroupKey: rememberedKey, LastSeen: 940},
	}}
	index := &countingHostBusiness{byIdentity: map[string]string{"10.0.0.9|0": "9"}}

	duePlans := []execution.DuePlan{{
		Identity:        execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		CompiledPlan:    noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"}}),
		StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
	}}
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports:  Ports{NoData: store, Hosts: index},
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10},
		},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}

	identity := execution.PlanNoDataIdentity{Plan: duePlans[0].Identity, StateGeneration: "state-v1"}
	resolution, resolved := stream.noDataHosts[identity]
	if !resolved {
		t.Fatalf("no host resolution for the Plan; the Slot has %+v", stream.noDataHosts)
	}
	// The remembered host is held under another business, so it left rather
	// than being expected - the check that stops a history roster reporting a
	// departed host absent for as long as the strategy lives.
	if _, expected := resolution.Known[remembered.IP+"|"+remembered.CloudID]; expected {
		t.Fatalf("a host of another business is expected: %+v", resolution.Known)
	}
	if _, left := resolution.OutOfBusiness[rememberedKey]; !left {
		t.Fatalf("out of business = %+v, want the remembered group keyed by %q",
			resolution.OutOfBusiness, rememberedKey)
	}
	if index.lookups != 1 {
		t.Fatalf("lookups = %d, want one for the single remembered host", index.lookups)
	}
}

// A cold index holds nothing, and nothing is expected. That is the safe
// direction: a warming index that reported every declared host absent would
// open an alert per host on every restart.
func TestResolvingNoDataHostsExpectsNothingFromAColdIndex(t *testing.T) {
	resolution := resolveNoDataHosts(&countingHostBusiness{}, "2", []nodata.HostCandidate{
		hostCandidate(t, "10.0.0.1", "0"),
	})
	if len(resolution.Known) != 0 || len(resolution.OutOfBusiness) != 0 {
		t.Fatalf("resolution = %+v, want nothing from an index that holds nothing", resolution)
	}
}

// A pass with no index says so, and does not look.
//
// The two sets it would produce are empty either way, so nothing about them
// distinguishes a process that has not built an index from a target whose
// hosts have all left this business. Resolved is the only thing that does,
// and the evaluation stops on it rather than judging an item against an
// expected set it has no basis for.
func TestResolvingNoDataHostsReportsAnIndexItCouldNotRead(t *testing.T) {
	cold := &countingHostBusiness{}
	candidates := []nodata.HostCandidate{hostCandidate(t, "10.0.0.1", "0"), hostCandidate(t, "10.0.0.2", "0")}

	resolution := resolveNoDataHosts(cold, "2", candidates)

	if resolution.Resolved {
		t.Fatal("Resolved is true with no index behind it: every host reads as one CMDB has never " +
			"heard of, which is indistinguishable from a target that resolved to nobody")
	}
	if cold.lookups != 0 {
		t.Fatalf("lookups = %d, want none: there is nothing to look in", cold.lookups)
	}
	if len(resolution.Known) != 0 || len(resolution.OutOfBusiness) != 0 {
		t.Fatalf("resolution = %+v, want both sets empty", resolution)
	}

	warm := &countingHostBusiness{byIdentity: map[string]string{"10.0.0.1|0": "2"}}
	if got := resolveNoDataHosts(warm, "2", candidates); !got.Resolved {
		t.Fatalf("resolution = %+v, want Resolved once there is an index to answer from", got)
	}
}
