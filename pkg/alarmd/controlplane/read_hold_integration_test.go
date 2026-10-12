package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
	"time"
)

func readHoldCatalog(t *testing.T, delay, threshold int) controlplane.Catalog {
	t.Helper()
	access := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &access, BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660"}}, SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}}})
	if err != nil {
		t.Fatal(err)
	}
	document := json.RawMessage(fmt.Sprintf(`{"id":101,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"fixture-query","expression":"a","unit":"","time_delay":%d,"query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"usage","alias":"a","agg_dimension":["host"],"agg_method":"MAX","agg_interval":60,"result_table_id":"system.cpu"}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":%d}]]}]}],"detects":[{"level":1,"priority":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`, delay, threshold))
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "101", Document: document, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: planner})
	if err != nil || len(catalog.QueryGroups) != 1 {
		t.Fatalf("catalog: %+v %v", catalog, err)
	}
	return catalog
}

func TestReadHoldLinkFollowsThePlanAcrossDelayGroupsAndEmptySegments(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:read-hold-links")
	first := readHoldCatalog(t, 0, 80)
	fixture.publish(t, first, 60)
	var original execution.QueryGroupIdentity
	var generation execution.StateGeneration
	for _, qg := range first.QueryGroups {
		if qg.Plans[0].Identity.BusinessID == "2" {
			original = qg.Identity
			generation = qg.Plans[0].StateGeneration
		}
	}
	if original == "" {
		t.Fatal("missing edited group")
	}
	var previous execution.QueryGroupIdentity = original
	for index, delay := range []int{120, 180} {
		catalog := readHoldCatalog(t, delay, 80)
		boundary := int64(121 + index)
		fixture.publish(t, catalog, boundary)
		var next controlplane.QueryGroup
		for _, qg := range catalog.QueryGroups {
			if qg.Plans[0].Identity.BusinessID == "2" {
				next = qg
			}
		}
		if next.Identity == previous || next.Identity == original {
			t.Fatal("delay edit kept query-group identity")
		}
		schedule, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, next.Identity, execution.EvaluationTime(boundary))
		if err != nil {
			t.Fatal(err)
		}
		refs, skipped, err := fixture.repository.ReadHoldPredecessors(fixture.ctx, schedule)
		if err != nil || len(skipped) != 0 || len(refs) != 1 || refs[0].QueryGroup != original || refs[0].ClosedAt != 121 || len(refs[0].Plans) != 1 {
			t.Fatalf("delay or empty intermediate lost predecessor: %+v %v %v", refs, skipped, err)
		}
		// The link says where the original group's last Slot was and when
		// it was due, across the empty intermediate Segment too.
		if link := refs[0].Plans[0]; link.PreviousSlot != 120 || link.CompletionOffsetMillis <= 0 {
			t.Fatalf("the link does not carry the original group's last Slot: %+v", link)
		}
		if next.Plans[0].StateGeneration != generation {
			t.Fatal("delay changed state generation")
		}
		for _, record := range fixture.activation(t).Plans {
			if record.Fact.Plan.BusinessID == "2" && record.Fact.Selected.StateGeneration == "" {
				t.Fatal("the activation names no state generation")
			}
		}
		previous = next.Identity
	}
	// A threshold edit in the last QG starts a state of its own -- the
	// algorithm's configuration is in the state generation -- so the original
	// group's Slots wrote a state this group no longer reads, there is no order
	// to keep, and the bridge is not carried on.
	activated := func() execution.StateGeneration {
		for _, record := range fixture.activation(t).Plans {
			if record.Fact.Plan.BusinessID == "2" {
				return record.Fact.Selected.StateGeneration
			}
		}
		t.Fatal("no activation for the edited Plan")
		return ""
	}
	before := activated()
	last := readHoldCatalog(t, 180, 90)
	fixture.publish(t, last, 180)
	if after := activated(); after == before || after == "" {
		t.Fatalf("a threshold edit kept the state generation %q; this case no longer tests a generation change", after)
	}
	schedule, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, previous, 180)
	if err != nil {
		t.Fatal(err)
	}
	refs, skipped, err := fixture.repository.ReadHoldPredecessors(fixture.ctx, schedule)
	if err != nil || len(skipped) != 0 || len(refs) != 0 {
		t.Fatalf("a bridge was carried into a new state generation: %+v %v %v", refs, skipped, err)
	}
}

// A stored link that names the group it is in, or that no cutover could
// have written, is skipped and counted by why: it can only have come from a
// fault, and refusing the whole group's links for it stopped the group.
func TestAStoredSelfOrMalformedLinkIsSkippedAndCounted(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:read-hold-bad-links")
	catalog := readHoldCatalog(t, 0, 80)
	fixture.publish(t, catalog, 60)
	group := catalog.QueryGroups[0].Identity
	key := fixture.prefix + ":schedule_timeline:" + string(group)
	raw, err := fixture.client.Get(fixture.ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	inject := func(link map[string]any) {
		t.Helper()
		var timeline map[string]any
		if err := json.Unmarshal(raw, &timeline); err != nil {
			t.Fatal(err)
		}
		segments := timeline["segments"].([]any)
		plans := segments[len(segments)-1].(map[string]any)["plans"].([]any)
		plans[0].(map[string]any)["previous_read_hold"] = link
		encoded, err := json.Marshal(timeline)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.client.Set(fixture.ctx, key, encoded, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	schedule, err := fixture.runtime.ReadFrozenSchedule(fixture.ctx, group, 60)
	if err != nil {
		t.Fatal(err)
	}
	for reason, link := range map[string]map[string]any{
		"self_link":    {"query_group": string(group), "closed_at": 60},
		"invalid_link": {"query_group": "other", "closed_at": 60, "previous_slot": 60},
	} {
		inject(link)
		// A fresh repository, as a process reading the stored timeline: the
		// fixture's caches the timeline by the control version.
		fresh, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		refs, skipped, err := fresh.ReadHoldPredecessors(fixture.ctx, schedule)
		if err != nil || len(refs) != 0 || skipped[reason] != 1 {
			t.Fatalf("%s: links %+v skipped %v err %v", reason, refs, skipped, err)
		}
	}
}
