// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package recentalert 保存 Elasticsearch 最近写入的 Alert 快照，用于跨越搜索 refresh 可见性窗口。
package recentalert

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func TestSnapshotCacheWritesKeepWireTTLAndTerminalAtomicity(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		client := newFakeRedis()
		config := Config{KeyPrefix: "snapshot-test", RefreshInterval: 5 * time.Second}
		cache, err := newStore(client, client.transaction, config, nil)
		if err != nil {
			t.Fatal(err)
		}
		status := domain.AlertStatusActive
		end := domain.AlertEndType("")
		if terminal {
			status = domain.AlertStatusRecovered
			end = domain.AlertEndTypeSource
		}
		current := cachedAlert("event", status, end)
		current.Alert.ExtraData = domain.JSONObject{"precise": json.RawMessage(`9007199254740993`)}
		before := current.Alert.Clone()
		full, err := encode(current)
		if err != nil {
			t.Fatal(err)
		}
		fast, err := encodeSnapshot(current)
		if err != nil {
			t.Fatal(err)
		}
		if string(full) != string(fast) {
			t.Fatal("cache wire changed")
		}
		if terminal {
			err = cache.PutTerminalFromSnapshot(t.Context(), current)
		} else {
			err = cache.PutCurrentFromSnapshot(t.Context(), current)
		}
		if err != nil {
			t.Fatal(err)
		}
		wantKeys := 1
		if terminal {
			wantKeys = 2
		}
		if len(client.values) != wantKeys {
			t.Fatal("terminal/current keys changed")
		}
		for key, payload := range client.values {
			if string(payload) != string(full) || client.expirations[key] != 10*time.Second {
				t.Fatal("payload/TTL changed")
			}
			got, err := decode(payload)
			if err != nil || !reflect.DeepEqual(got.Alert, before) {
				t.Fatalf("cache read differs: %v", err)
			}
		}
		if !reflect.DeepEqual(current.Alert, before) {
			t.Fatal("cache encoder mutated snapshot")
		}
	}
}

func TestSnapshotCacheRejectsInvalidStateTokenEncodingAndBudget(t *testing.T) {
	for _, name := range []string{"state", "token", "json", "budget"} {
		t.Run(name, func(t *testing.T) {
			client := newFakeRedis()
			cache, err := newStore(client, client.transaction, Config{KeyPrefix: "snapshot-invalid", RefreshInterval: time.Second}, nil)
			if err != nil {
				t.Fatal(err)
			}
			current := cachedAlert("event", domain.AlertStatusActive, "")
			switch name {
			case "state":
				current.Alert.Revision = 0
			case "token":
				current.Version = store.VersionToken{}
			case "json":
				current.Alert.ExtraData = domain.JSONObject{"bad": json.RawMessage(`{`)}
			case "budget":
				current.Alert.ExtraData = domain.JSONObject{"large": json.RawMessage(`"` + strings.Repeat("x", maxEntryBytes) + `"`)}
			}
			if err := cache.PutCurrentFromSnapshot(t.Context(), current); err == nil || len(client.values) != 0 {
				t.Fatal("invalid cache write accepted")
			}
		})
	}
	// 普通写入仍完整检查 enrich 的语义，不因增加内部快照路径而放松入口。
	bad := cachedAlert("event", domain.AlertStatusActive, "")
	bad.Alert.EnrichStatus = domain.EnrichStatusSucceeded
	bad.Alert.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"bad":{"status":"succeeded"}}]`)}
	if _, err := encode(bad); err == nil {
		t.Fatal("public cache encoder bypassed payload validation")
	}
}

func BenchmarkRecentAlertEncoding(b *testing.B) {
	current := cachedAlert("event", domain.AlertStatusActive, "")
	current.Alert.ExtraData = domain.JSONObject{"large": json.RawMessage(`"` + strings.Repeat("x", 128<<10) + `"`)}
	var err error
	current.Alert, err = current.Alert.Normalize()
	if err != nil {
		b.Fatal(err)
	}
	for _, fast := range []bool{false, true} {
		name := "full"
		if fast {
			name = "snapshot"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if fast {
					_, err = encodeSnapshot(current)
				} else {
					_, err = encode(current)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
