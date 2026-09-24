package controlplane_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func TestG4LegacyQueryCompilerFreezesIdentityOverrideAndRawHistoryVariant(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	source := controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "1001", ItemID: "1", QueryMD5: "query-md5", Expression: "a <= 3600",
		IdentityFields: []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
		Functions:      []json.RawMessage{json.RawMessage(`{"id":"abs","params":[]}`)},
		QueryConfigs: []json.RawMessage{json.RawMessage(`{
			"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"proc_exists",
			"alias":"a","agg_dimension":["protocol","listen","nonlisten","not_accurate_listen","bind_ip","bk_target_ip","bk_target_cloud_id","display_name"],
			"agg_method":"MAX","agg_interval":60,"result_table_id":"system.proc_port"
		}`)},
	}
	primary, err := planner.CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got := primary.Normalization.DatasetContract.IdentityFields; !reflect.DeepEqual(got, source.IdentityFields) {
		t.Fatalf("identity fields = %v, want %v", got, source.IdentityFields)
	}
	raw, err := planner.CompileAlgorithmDependencyQuery(context.Background(), source, "a")
	if err != nil {
		t.Fatal(err)
	}
	if raw.MetricMerge != "a" || primary.MetricMerge != "abs(a <= 3600)" || raw.QueryRevision == primary.QueryRevision {
		t.Fatalf("primary=%+v raw=%+v", primary, raw)
	}
}
