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
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A strategy's unit is read from where the strategy cache keeps it, and a
// threshold prefix configured against it decides the level's fate.
//
// The cache has no unit on an item. Python's Item.unit is a derived property
// that walks query_configs and takes the first non-empty one; alarmd read a
// "unit" key on the item itself, which no stored document has. Every threshold
// configured with a unit prefix then compiled against an empty data unit,
// failed to find its prefix among that unit's suffixes, and took the whole
// level out as LEVEL_INVALID. On one deployment that was 327 strategies --
// 11.5% of all of them -- none of which had ever evaluated, and the only
// symptom was a count of withheld objects that named no field.
//
// Driven from a document through publication to the audit, because that is the
// chain that was broken and every link in it looked healthy. The multipliers
// the two units and prefixes produce are pinned next to the rule itself, in
// the strategy package; what this test adds is that the unit reaches it at
// all, and what happens to the level when it does not.
func TestAThresholdUnitIsReadFromTheQueryConfigWhereTheCacheKeepsIt(t *testing.T) {
	for _, test := range []struct {
		name   string
		unit   string
		prefix string
		// refused is the level being taken out of service, which is what the
		// missing unit did to every one of these.
		refused bool
	}{
		{name: "a percentage with a percent prefix", unit: "percent", prefix: "%"},
		{name: "bits per second with a mega prefix", unit: "bps", prefix: "M"},
		{name: "bytes with a gibi prefix", unit: "bytes", prefix: "Gi"},
		// A unit with no scale of its own. Python converts nothing for these,
		// so the prefix is pointless rather than wrong, and the strategy runs.
		{name: "no unit at all with a percent prefix", unit: "", prefix: "%"},
		// And the one that must still be refused: a unit that does have a
		// scale, given a prefix that is not one of its own. Here the prefix
		// changes the number, and accepting an unrecognised one would compare
		// against a threshold nobody meant.
		{name: "bits per second with a gibi prefix", unit: "bps", prefix: "Gi", refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newControlplaneRedis(t)
			ctx := context.Background()
			document := withItemUnitAndThresholdPrefix(t, realThresholdDocuments(t)[0], test.unit, test.prefix)
			if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
				t.Fatal(err)
			}
			if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
				t.Fatal(err)
			}
			source := newRedisStrategySource(t, client)
			planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
			if err != nil {
				t.Fatal(err)
			}
			repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:item-unit", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			compiler, stateSemantics := runtimePlanCompiler(t)
			reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := reconciler.Refresh(ctx, source, planner); err != nil ||
				result.Status != controlplane.SourceRefreshPendingConfirmation {
				t.Fatalf("first refresh = (%#v, %v)", result, err)
			}
			published, err := reconciler.Refresh(ctx, source, planner)
			if err != nil || published.Status != controlplane.SourceRefreshPublished {
				t.Fatalf("publish = (%#v, %v)", published, err)
			}

			if test.refused {
				assertAuditDispositionExact(t, repository, "1001", "LEVEL", 1,
					controlplane.DispositionConfigRejected, contract.ReasonLevelInvalid)
				// And the refusal says which field. Without it the reader has a
				// reason word for a document of a few hundred keys, and the only
				// way to find the key is to compile the document again offline.
				assertAuditDispositionField(t, repository, "1001", "LEVEL", 1, "level.detect_plan.algorithms")
				return
			}
			assertAuditDispositionExact(t, repository, "1001", "PLAN", 0, controlplane.DispositionAccepted, "")
			assertNoAuditDisposition(t, repository, "1001", controlplane.DispositionConfigRejected)

			// The unit itself reached the compiled Plan. Acceptance alone
			// cannot say so: an empty unit accepts an empty prefix too, and
			// accepts any prefix now that a scaleless unit ignores it -- so a
			// test that stopped at "accepted" would pass on exactly the defect
			// it is here to catch.
			snapshot, err := loadPublishedSnapshot(ctx, repository, published.Publication)
			if err != nil {
				t.Fatal(err)
			}
			plans := plansByStrategy(snapshot)
			if got := plans["1001"].Plan.InputProjection.DataUnit; got != test.unit {
				t.Fatalf("the compiled Plan carries data unit %q, want %q from the query config. The unit "+
					"is where the cache keeps it, and reading it anywhere else finds nothing on every "+
					"strategy the platform stores", got, test.unit)
			}
		})
	}
}

// withItemUnitAndThresholdPrefix puts the unit on the item's query configs,
// which is where the strategy cache keeps it, and gives the level's threshold
// algorithm the prefix. It deliberately leaves no unit on the item: a document
// that carried one would be a document the platform does not write.
func withItemUnitAndThresholdPrefix(
	t *testing.T,
	document json.RawMessage,
	unit, prefix string,
) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	if _, present := item["unit"]; present {
		t.Fatal("the fixture carries a unit on the item, which no stored strategy document does")
	}
	for _, raw := range item["query_configs"].([]any) {
		raw.(map[string]any)["unit"] = unit
	}
	for _, raw := range item["algorithms"].([]any) {
		raw.(map[string]any)["unit_prefix"] = prefix
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertAuditDispositionField(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	scope string,
	levelID uint32,
	field string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Scope == scope && item.LevelID == levelID {
			if item.FieldPath != field {
				t.Fatalf("disposition names field %q, want %q", item.FieldPath, field)
			}
			return
		}
	}
	t.Fatalf("no disposition for source=%s scope=%s level=%d: %#v", sourceID, scope, levelID, audit)
}
