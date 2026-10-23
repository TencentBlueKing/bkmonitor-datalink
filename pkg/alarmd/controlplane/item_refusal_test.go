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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// An item the catalog refuses before compiling it is refused under the name
// of what is wrong with it. A data source this build does not compile is a
// capability fact whatever else the item carries - the writer publishes those
// types with no query_md5 at all - so it is judged first and named as the
// source it is. Only a supported item that lacks a field is a configuration
// left incomplete, one name per field, with the field.
func TestAnItemIsRefusedUnderTheNameOfWhatIsWrongWithIt(t *testing.T) {
	const timeSeries = `{"data_source_label":"bk_monitor","data_type_label":"time_series","agg_interval":60}`
	const algorithms = `[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":1}]]}]`
	for _, testCase := range []struct {
		name                            string
		md5, expression, configs, algos string
		disposition                     controlplane.Disposition
		reason, field                   string
	}{
		{name: "an event source, with no query_md5 as the writer publishes it", md5: ``, expression: `a`,
			configs: `[{"data_source_label":"bk_monitor","data_type_label":"event","agg_interval":60}]`, algos: algorithms,
			disposition: controlplane.DispositionUnsupported, reason: "QUERY_SOURCE_NOT_MIGRATED", field: "items[0].query_configs[0]"},
		{name: "an FTA source", md5: ``, expression: `a`,
			configs: `[{"data_source_label":"bk_fta","data_type_label":"event","agg_interval":60}]`, algos: algorithms,
			disposition: controlplane.DispositionUnsupported, reason: "QUERY_FTA_UNSUPPORTED", field: "items[0].query_configs[0]"},
		{name: "a supported source without query_md5", md5: ``, expression: `a`, configs: `[` + timeSeries + `]`, algos: algorithms,
			disposition: controlplane.DispositionConfigRejected, reason: controlplane.ReasonItemQueryMD5Missing, field: "items[0].query_md5"},
		{name: "no algorithm", md5: `q`, expression: `a`, configs: `[` + timeSeries + `]`, algos: `[]`,
			disposition: controlplane.DispositionConfigRejected, reason: controlplane.ReasonItemAlgorithmsMissing, field: "items[0].algorithms"},
		{name: "no query config", md5: `q`, expression: `a`, configs: `[]`, algos: algorithms,
			disposition: controlplane.DispositionConfigRejected, reason: controlplane.ReasonItemQueryConfigsMissing, field: "items[0].query_configs"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			document := fmt.Sprintf(`{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":%q,"expression":%q,`+
				`"query_configs":%s,"algorithms":%s}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":5},"recovery_config":{"check_window":3}}]}`,
				testCase.md5, testCase.expression, testCase.configs, testCase.algos)
			catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: json.RawMessage(document),
					Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
				Planner: &recordingPlanner{facts: queryFacts(t)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 {
				t.Fatalf("groups %d, dispositions %+v: want the item refused once", len(catalog.QueryGroups), catalog.Dispositions)
			}
			got := catalog.Dispositions[0]
			if got.Disposition != testCase.disposition || got.Reason != testCase.reason || got.FieldPath != testCase.field {
				t.Fatalf("refused as %s %s at %s, want %s %s at %s", got.Disposition, got.Reason, got.FieldPath,
					testCase.disposition, testCase.reason, testCase.field)
			}
		})
	}
}

// An item that carries no expression is not refused: the platform's query
// reads an empty expression as its queries' reference names joined by "or",
// which for an item's one query is that query. It compiles to the query the
// same item with the expression written out compiles to.
func TestAnItemWithoutAnExpressionRunsItsQueryAsThePlatformDoes(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	merge := func(t *testing.T, expression string) string {
		t.Helper()
		document := fmt.Sprintf(`{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":%q,`+
			`"query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary",`+
			`"metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,"agg_dimension":["bk_target_ip","bk_target_cloud_id"],`+
			`"agg_condition":[],"functions":[]}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":1}]]}]}],`+
			`"detects":[{"level":1,"trigger_config":{"count":1,"check_window":5},"recovery_config":{"check_window":3}}]}`, expression)
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: json.RawMessage(document),
				Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
			Planner: planner,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.QueryGroups) != 1 {
			t.Fatalf("expression %q: dispositions %+v, want one Plan", expression, catalog.Dispositions)
		}
		return catalog.QueryGroups[0].QueryPlan.MetricMerge
	}
	if empty, written := merge(t, ""), merge(t, "a"); empty != "a" || written != empty {
		t.Fatalf("metric merge without an expression %q, with it written %q, want both a", empty, written)
	}
}
