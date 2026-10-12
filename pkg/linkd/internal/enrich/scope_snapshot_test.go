// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
)

func TestScopePreservesStoredJSONAndIsolatesMutation(t *testing.T) {
	event := testAlert()
	raw := json.RawMessage(` {"z":9007199254740993, "a":{"legacy":true}} `)
	event.SourceRawData = domain.JSONObject{"source": raw}
	scope, err := NewScope(event, Sources{})
	if err != nil {
		t.Fatal(err)
	}
	got := scope.Event()
	if !reflect.DeepEqual(got.SourceRawData["source"], raw) {
		t.Fatal("scope re-encoded source JSON")
	}
	got.SourceRawData["source"][0] = '!'
	if !reflect.DeepEqual(scope.Event().SourceRawData["source"], raw) || event.SourceRawData["source"][0] == '!' {
		t.Fatal("scope shares mutable source bytes")
	}
}
