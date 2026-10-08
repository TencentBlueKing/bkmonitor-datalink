// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
)

func TestRefreshTaskSchemaRedis(t *testing.T) {
	cache, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	schema, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close()
	schema.Set("bkmonitorv3:entity:RelationDefinition", "metadata")

	for _, test := range []struct {
		name       string
		schemaAddr string
		want       string
	}{
		{name: "metadata schema Redis", schemaAddr: schema.Addr(), want: "metadata"},
		{name: "legacy task fallback", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := RefreshTaskParams{Redis: redis.Options{Mode: "standalone", Addrs: []string{cache.Addr()}}}
			if test.schemaAddr != "" {
				params.SchemaRedis = &redis.Options{Mode: "standalone", Addrs: []string{test.schemaAddr}}
			}
			payload, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			var decoded RefreshTaskParams
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			client, err := redis.GetClient(decoded.schemaRedisOptions())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			got, _ := client.Get(context.Background(), "bkmonitorv3:entity:RelationDefinition").Result()
			if got != test.want {
				t.Fatalf("schema Redis value = %q, want %q", got, test.want)
			}
		})
	}
}
