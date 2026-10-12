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
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

const (
	refusalQuery     = `{"agg_interval":60}`
	refusalThreshold = `{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":1}]]}`
	refusalRecovery  = `{"check_window":3}`
)

func refusalCatalog(t *testing.T, query, algorithm, recovery string) controlplane.Catalog {
	t.Helper()
	document := fmt.Sprintf(`{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a",`+
		`"query_configs":[%s],"algorithms":[%s]}],`+
		`"detects":[{"level":1,"trigger_config":{"count":1,"check_window":5},"recovery_config":%s}]}`, query, algorithm, recovery)
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: json.RawMessage(document),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// A refusal whose word is a catch-all, or whose check produced an error, says
// what went wrong in the audit a strategy lookup reads. Before this the text
// was dropped: two strategies refused as PLAN_INVALID had to be compiled again
// offline to learn their expression was empty.
func TestARefusalBuiltFromAnErrorCarriesWhatTheErrorSaid(t *testing.T) {
	tests := []struct {
		name                         string
		query, algorithm, recovery   string
		scope, reason, detailMention string
	}{
		{"the catch-all of a plan that did not build", `{"agg_interval":-60}`, refusalThreshold, refusalRecovery,
			"PLAN", "PLAN_INVALID", "interval"},
		{"a threshold the compiler refused", refusalQuery,
			`{"level":1,"type":"Threshold","config":[[{"method":"bogus","threshold":1}]]}`, refusalRecovery,
			"LEVEL", "THRESHOLD_CONFIG_INVALID", "Threshold"},
		{"a fixed algorithm over another query", `{"agg_interval":60,"result_table_id":"system.cpu"}`,
			`{"level":1,"type":"ProcPort","config":{}}`, refusalRecovery,
			"LEVEL", "ALGORITHM_QUERY_INVALID", "canonical"},
		{"a recovery with no window", refusalQuery, refusalThreshold, `{"check_window":0}`,
			"LEVEL", "RECOVERY_CONFIG_INVALID", "check_window"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := refusalCatalog(t, test.query, test.algorithm, test.recovery)
			var found *controlplane.ObjectDisposition
			for index := range catalog.Dispositions {
				if catalog.Dispositions[index].Reason == test.reason && catalog.Dispositions[index].Scope == test.scope {
					found = &catalog.Dispositions[index]
				}
			}
			if found == nil {
				t.Fatalf("no %s %s among %+v", test.scope, test.reason, catalog.Dispositions)
			}
			if found.Detail == "" || !strings.Contains(found.Detail, test.detailMention) {
				t.Fatalf("detail = %q, want the error's own account (mentioning %q)", found.Detail, test.detailMention)
			}
			// The same document refused the same way twice: the text is part
			// of the audit a round compares with the published one, and one
			// that moved between rounds would make every round a publication.
			again := refusalCatalog(t, test.query, test.algorithm, test.recovery)
			if !reflect.DeepEqual(again.Dispositions, catalog.Dispositions) {
				t.Fatalf("a second build refused differently:\n%+v\n%+v", catalog.Dispositions, again.Dispositions)
			}
		})
	}
	// An accepted strategy carries no text.
	for _, disposition := range refusalCatalog(t, refusalQuery, refusalThreshold, refusalRecovery).Dispositions {
		if disposition.Detail != "" {
			t.Fatalf("an accepted strategy carries %+v", disposition)
		}
	}
}
