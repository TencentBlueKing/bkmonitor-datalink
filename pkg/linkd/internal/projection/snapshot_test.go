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
	"encoding/json"
	"fmt"
	"testing"
)

func TestRestoreSnapshotAcceptsReorderedObjectsButRejectsChangedContent(t *testing.T) {
	canonical := []byte(`{"a":9007199254740991,"z":{"first":1,"second":2}}`)
	hash := fmt.Sprintf("%x", sha256.Sum256(canonical))
	reordered := []byte(`{"z":{"second":2,"first":1},"a":9007199254740991}`)
	got, err := RestoreSnapshot(reordered, hash)
	if err != nil || string(got) != string(canonical) {
		t.Fatal("ES serialization changed frozen identity", string(got), err)
	}
	for _, bad := range []string{`{"z":{"second":3,"first":1},"a":9007199254740991}`, `{"a":9007199254740990,"z":{"first":1,"second":2}}`, `{} {}`} {
		if _, err := RestoreSnapshot([]byte(bad), hash); err == nil {
			t.Fatal("modified snapshot accepted", bad)
		}
	}
}

func TestStoredSnapshotKeepsNumericSpellingAndRestoresWireObject(t *testing.T) {
	original := []byte(`{"a":1.200,"b":9007199254740991}`)
	hash := fmt.Sprintf("%x", sha256.Sum256(original))
	raw, err := PreserveSnapshot([]byte(`{"request":{"alert":`+string(original)+`}}`), "request")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Request struct {
			Alert json.RawMessage `json:"alert"`
		} `json:"request"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Request.Alert[0] != '"' {
		t.Fatal("snapshot remains mutable JSON object", string(raw))
	}
	restored, err := RestoreSnapshot(envelope.Request.Alert, hash)
	if err != nil || string(restored) != string(original) {
		t.Fatal("frozen numeric representation changed", string(restored), err)
	}
}
