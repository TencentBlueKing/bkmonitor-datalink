// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProjectionRequestRejectsInternalIntentMetadata(t *testing.T) {
	_, _, _, _, _, _, a := projectionFixture(t)
	for _, field := range []string{"projection", "policy_change", "merge_change", "action_pending"} {
		t.Run(field, func(t *testing.T) {
			request, err := BuildRequest(a, "kac")
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(request.Alert, &body); err != nil {
				t.Fatal(err)
			}
			body[field] = json.RawMessage(`null`)
			request.Alert, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(request.Alert)
			request.ContentHash = hex.EncodeToString(hash[:])
			if !errors.Is(request.Validate(), ErrInvalid) {
				t.Fatal("internal intent accepted on projection wire", field)
			}
		})
	}
}

func TestProjectionTaskIDRetainsV1Identity(t *testing.T) {
	id, err := TaskID("tenant", "alert", "kac", 1)
	if err != nil || id != "6e00584301d81d93a64c420d15dc7846925c309224c2f0389ccc1be5cd3dffa4" {
		t.Fatal("V1 persisted identity changed", id, err)
	}
	for _, v := range []struct {
		tenant, alert, target string
		revision              int64
	}{
		{"", "alert", "kac", 1}, {"tenant", "", "kac", 1}, {"tenant", "alert", "", 1},
		{"tenant", "alert", "kac", 0}, {"tenant", "alert", "kac", 1 << 53},
		{"tenant", "alert", strings.Repeat("a", 65), 1},
	} {
		if id, err := TaskID(v.tenant, v.alert, v.target, v.revision); id != "" || !errors.Is(err, ErrInvalid) {
			t.Fatal(id, err)
		}
	}
	other, _ := TaskID("other", "alert", "kac", 1)
	target, _ := TaskID("tenant", "alert", "other", 1)
	revision, _ := TaskID("tenant", "alert", "kac", 2)
	if id == other || id == target || id == revision {
		t.Fatal("identity scope collision")
	}
}
