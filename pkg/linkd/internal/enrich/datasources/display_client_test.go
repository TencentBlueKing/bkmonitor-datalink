// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/enrich"
)

type displayCacheStub struct {
	values map[string]string
	err    error
}

func (s displayCacheStub) HStrLen(_ context.Context, key, field string) *redis.IntCmd {
	return redis.NewIntResult(int64(len(s.values[key+"/"+field])), s.err)
}

func (s displayCacheStub) HGet(_ context.Context, key, field string) *redis.StringCmd {
	return redis.NewStringResult(s.values[key+"/"+field], s.err)
}

func (s displayCacheStub) GetRange(_ context.Context, key string, _, _ int64) *redis.StringCmd {
	return redis.NewStringResult(s.values[key], s.err)
}

type displayModelStub struct{}

func (displayModelStub) GetModelByCode(_ context.Context, tenant, model string) (enrich.Model, bool, error) {
	return enrich.Model{TenantID: tenant, ModelCode: model, Fields: map[string]any{"bk_cmdb_obj_id": "host"}}, true, nil
}

func TestDisplayFormattingTenantAndFullOrganizationID(t *testing.T) {
	cache := displayCacheStub{values: map[string]string{"p:cmdb_object_attribute_cache_key:t/host": `{"enum":{"status":{"0":"禁用"}},"num":{"size":"GB"},"time":["created"],"objuser":["owner"],"organization":["department"]}`, "p:sync_organization_all_staff:t/alice": `{"username":"alice","display_name":"艾丽丝"}`, "p:sync_organization_all_department:t": `[{"id":"123","full_name":"组织/研发/后端"}]`, "p:cmdb_cloud_display_cache_key:t": `{"kv":{"0":"默认区域"}}`}}
	client := NewDisplayClient(cache, displayModelStub{}, "p:")
	for _, tc := range []struct {
		field string
		value any
		want  string
	}{{"status", json.Number("0"), "禁用"}, {"size", json.Number("0"), "0GB"}, {"created", "2026-09-22T10:11:12.000+08:00", "2026-09-22 10:11:12"}, {"owner", "alice,bob", "alice(艾丽丝);bob"}, {"department", "123", "研发/后端"}, {"bk_cloud_id", json.Number("0"), "默认区域[0]"}} {
		got, err := client.Format(t.Context(), "t", "cw-Host", tc.field, tc.value)
		if err != nil || got != tc.want {
			t.Errorf("%s=%v err=%v", tc.field, got, err)
		}
	}
	got, err := client.Format(t.Context(), "other", "cw-Host", "status", json.Number("0"))
	if err != nil || got != json.Number("0") {
		t.Fatal("cross tenant cache reused")
	}
	client = NewDisplayClient(displayCacheStub{err: errors.New("redis failure")}, displayModelStub{}, "")
	if _, err := client.Format(t.Context(), "t", "cw-Host", "owner", "alice"); err == nil {
		t.Fatal("cache error swallowed")
	}
}
