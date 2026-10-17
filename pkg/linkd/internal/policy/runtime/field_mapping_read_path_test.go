// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store/storetest"
)

func TestCandidateReadCacheKeepsJSONPathSelectionOrder(t *testing.T) {
	event := storetest.Event("tenant", "event", "fingerprint", "warning")
	event.Labels["owner"] = domain.NewStringScalar("builtin")
	event.ExtraData["owner"] = json.RawMessage(`"custom"`)
	event, err := event.WithEnrichment(storetest.Enrichment(event))
	if err != nil {
		t.Fatal(err)
	}
	reader := eventCandidateReader(event, nil)
	for _, test := range []struct{ path, want string }{
		{`$['extra_data','labels'].owner`, "custom"},
		{`$['labels','extra_data'].owner`, "builtin"},
		{`$['extra_data','labels'].owner`, "custom"},
	} {
		value, err := reader(t.Context(), "owner", policy.FieldMapping{Path: "$.extra_data.owner", ReadPath: test.path, Kind: policy.FieldText})
		if err != nil || !value.Present || !reflect.DeepEqual(value.Data, []any{test.want}) {
			t.Fatalf("read %s: %+v %v", test.path, value, err)
		}
	}
}
