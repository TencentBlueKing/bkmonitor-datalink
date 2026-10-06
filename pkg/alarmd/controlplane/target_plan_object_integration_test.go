// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// objectCatalogTwoGroupsWithTargetPlan is objectCatalogTwoGroups with a
// target_plan on the business-3 strategy.
func objectCatalogTwoGroupsWithTargetPlan(t *testing.T) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(withWireIdentity(t, documents[1], "tenant-a", "bkcc__3"), &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["bk_biz_id"] = json.RawMessage(`3`)
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(decoded["items"], &items); err != nil {
		t.Fatal(err)
	}
	items[0]["target_plan"] = json.RawMessage(`{"schema_version":1,"model_id":"cw-Host","target_rule":"host_id","failure_policy":"no_match",
		"static_targets":[{"bk_host_id":101}],"dynamic_groups":[{"dynamic_group_id":"1001"}],"dynamic_topologies":[]}`)
	decoded["items"], _ = json.Marshal(items)
	documentB, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3")}}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documents[0], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		{SourceID: "1002", Document: documentB, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
	}, Planner: planner, TargetSources: controlplane.TargetSources{DynamicGroups: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 2 {
		t.Fatalf("expected two Query Groups, got %d with dispositions %+v", len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

// A Query Group whose Plan carries a target plan is stored as a v2 object
// under a digest derived in the v2 domain, and reads back with the target
// plan on the Plan; the Query Group without one is the v1 object it always
// was, byte for byte. A stored object of a version this build does not know
// is refused before its digest is checked - the check a reader that
// predates the field makes on a v2 object.
func TestAnObjectCarryingATargetPlanIsWrittenAndReadUnderTheV2Contract(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	before := objectCatalogTwoGroups(t, 80)
	catalog := objectCatalogTwoGroupsWithTargetPlan(t)
	harness.publish(t, catalog)

	for _, group := range catalog.QueryGroups {
		object := controlplane.BuildQueryGroupObject(group)
		digest, err := controlplane.DeriveQueryGroupObjectDigest(group)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := harness.client.Get(harness.ctx, harness.prefix+":qgobj:"+string(digest)).Bytes()
		if err != nil {
			t.Fatalf("object %s: %v", digest, err)
		}
		var header struct {
			ContractVersion string `json:"object_contract_version"`
		}
		if err := json.Unmarshal(payload, &header); err != nil {
			t.Fatal(err)
		}
		carries := group.Plans[0].Plan.TargetPlan != nil
		wantVersion := "alarmd-query-group-object-v1"
		if carries {
			wantVersion = "alarmd-query-group-object-v2"
		}
		if header.ContractVersion != wantVersion || object.ContractVersion != wantVersion {
			t.Fatalf("stored %s / built %s, want %s for a group whose Plan carries a target plan = %v", header.ContractVersion, object.ContractVersion, wantVersion, carries)
		}
		hashed, err := contract.DeriveCanonicalDigestV2OverCanonical(wantVersion, payload)
		if err != nil || execution.ObjectDigest(hashed) != digest {
			t.Fatalf("stored object hashes to %s in its own domain, want %s (%v)", hashed, digest, err)
		}
		if !carries {
			// The v1 object is exactly the one the same strategy produced
			// before any target plan existed in the catalog.
			var previous execution.ObjectDigest
			for _, old := range before.QueryGroups {
				if old.Identity == group.Identity {
					previous, _ = controlplane.DeriveQueryGroupObjectDigest(old)
				}
			}
			if previous != digest {
				t.Fatalf("the untouched Query Group's digest moved from %s to %s", previous, digest)
			}
		}
		loaded, err := harness.repository.LoadQueryGroupObject(harness.ctx, digest)
		if err != nil {
			t.Fatalf("load %s: %v", digest, err)
		}
		if !reflect.DeepEqual(loaded.Plans[0].TargetPlan, group.Plans[0].Plan.TargetPlan) {
			t.Fatalf("loaded target plan %+v, want %+v", loaded.Plans[0].TargetPlan, group.Plans[0].Plan.TargetPlan)
		}
		if carries {
			assembled, err := controlplane.AssembleQueryGroup(loaded, outputContextsFor(t, harness, group))
			if err != nil || !reflect.DeepEqual(assembled.Plans[0].Plan.TargetPlan, group.Plans[0].Plan.TargetPlan) || assembled.Plans[0].Plan.TargetScope != nil {
				t.Fatalf("assembled %+v (%v), want the target plan back on the Plan and no scope", assembled.Plans[0].Plan.TargetPlan, err)
			}
			// A reader that only knows v1 keys the object by the version
			// before anything else; this build does the same for a version
			// it does not know, so the refusal is by name, not by digest.
			// A later version of this contract is refused as newer, which
			// is a rollout; a version that is not of this contract at all
			// is refused as not an object.
			foreign := strings.Replace(string(payload), wantVersion, "alarmd-query-group-object-v999", 1)
			if err := harness.client.Set(harness.ctx, harness.prefix+":qgobj:"+string(digest), foreign, 0).Err(); err != nil {
				t.Fatal(err)
			}
			fresh := harness.newRepository(t)
			if _, err := fresh.LoadQueryGroupObject(harness.ctx, digest); !errors.Is(err, controlplane.ErrCatalogObjectContractNewer) || errors.Is(err, controlplane.ErrCatalogObjectCorrupt) {
				t.Fatalf("an object of a later contract loaded or was refused as something else: %v", err)
			}
			foreign = strings.Replace(string(payload), wantVersion, "alarmd-target-group-object-v1", 1)
			if err := harness.client.Set(harness.ctx, harness.prefix+":qgobj:"+string(digest), foreign, 0).Err(); err != nil {
				t.Fatal(err)
			}
			fresh = harness.newRepository(t)
			if _, err := fresh.LoadQueryGroupObject(harness.ctx, digest); !errors.Is(err, controlplane.ErrCatalogObjectCorrupt) || !strings.Contains(err.Error(), "of this contract") {
				t.Fatalf("an object of another contract loaded or failed for another reason: %v", err)
			}
		}
	}
}

func outputContextsFor(t *testing.T, harness *objectCatalogHarness, group controlplane.QueryGroup) map[execution.PlanIdentity]controlplane.OutputContextObject {
	t.Helper()
	contexts := make(map[execution.PlanIdentity]controlplane.OutputContextObject, len(group.Plans))
	for _, plan := range group.Plans {
		digest, err := controlplane.DeriveOutputContextDigest(plan)
		if err != nil {
			t.Fatal(err)
		}
		context, err := harness.repository.LoadOutputContext(harness.ctx, digest)
		if err != nil {
			t.Fatal(err)
		}
		contexts[plan.Identity] = context
	}
	return contexts
}
