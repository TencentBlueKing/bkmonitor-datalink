// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// An index is read whole a page at a time. With entries the size the index
// writes, the hash stays in its hashtable encoding, so one HSCAN of
// noDataEndScanCount fields returns a page and a cursor rather than the
// whole hash; ScanHash follows the cursor to the end and returns every
// field once, at any page size. A key that does not exist reads as nothing.
func TestAnIndexIsReadWholeOnePageAtATime(t *testing.T) {
	address := reserveTCPAddress(t)
	startRedisServer(t, redistest.Server(t), address)
	backend, err := NewRedisBackend(RedisBackendOptions{Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	ctx := context.Background()
	const key, entries = "alarmd:nodata-end:scan", 2500
	puts := make(map[string][]byte, entries)
	for index := 0; index < entries; index++ {
		field := fmt.Sprintf("%064x:1", index)
		puts[field] = []byte(fmt.Sprintf(`{"f":"%032x","d":{"bk_target_ip":"192.0.2.%d","bk_target_cloud_id":"0"},"t":1792741200}`, index, index%250))
	}
	if err := backend.WriteHashFields(ctx, key, puts, nil, time.Hour); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })
	if encoding, err := client.ObjectEncoding(ctx, key).Result(); err != nil || encoding != "hashtable" {
		t.Fatalf("encoding = %q, %v; want hashtable, so a scan pages", encoding, err)
	}
	if page, cursor, err := client.HScan(ctx, key, 0, "", noDataEndScanCount).Result(); err != nil || cursor == 0 || len(page)/2 >= entries {
		t.Fatalf("one HSCAN returned %d fields with cursor %d (%v), want a page short of %d", len(page)/2, cursor, err, entries)
	}
	for _, count := range []int64{noDataEndScanCount, 7} {
		read, err := backend.ScanHash(ctx, key, count)
		if err != nil || len(read) != entries {
			t.Fatalf("count %d: read %d fields (%v), want all %d", count, len(read), err, entries)
		}
		for field, value := range puts {
			if string(read[field]) != string(value) {
				t.Fatalf("count %d: field %s = %q, want %q", count, field, read[field], value)
			}
		}
	}
	if read, err := backend.ScanHash(ctx, "alarmd:nodata-end:absent", noDataEndScanCount); err != nil || read != nil {
		t.Fatalf("an absent key read %v, %v; want nothing", read, err)
	}
	if _, err := backend.ScanHash(ctx, key, 0); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("a scan of count 0 = %v, want refused", err)
	}
}
