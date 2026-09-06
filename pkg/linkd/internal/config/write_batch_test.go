// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWriteBatchDerivedLimits(t *testing.T) {
	for _, tc := range []struct{ concurrency, operations, wait, readWait, parallel int }{
		{1, 1, 0, 0, 1}, {2, 1, 0, 0, 2}, {3, 1, 0, 0, 3}, {4, 2, 2, 2, 4}, {8, 4, 4, 4, 8}, {32, 16, 16, 10, 32}, {64, 32, 32, 10, 32}, {255, 127, 127, 10, 32}, {256, 128, 128, 10, 32}, {384, 128, 128, 10, 32}, {512, 128, 128, 10, 32}, {1024, 128, 128, 10, 32},
	} {
		c := (LifecycleConfig{Concurrency: tc.concurrency}).WithDefaults()
		b := c.ElasticsearchWriteBatch
		if b.MaxOperations != tc.operations || b.WaitMilliseconds != tc.wait || b.ReadWaitMilliseconds != tc.readWait || b.MaxConcurrentBatches != tc.parallel || b.MaxBytes != 4<<20 || !*b.Enabled {
			t.Fatalf("concurrency=%d batch=%+v", tc.concurrency, b)
		}
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWriteBatchRecomputesAfterConcurrencyChange(t *testing.T) {
	c := (LifecycleConfig{}).WithDefaults()
	c.Concurrency = 256
	c = c.WithDefaults()
	if c.ElasticsearchWriteBatch.MaxOperations != 128 {
		t.Fatal("stale derived batch size")
	}
	c.Concurrency = 1
	c = c.WithDefaults()
	if c.ElasticsearchWriteBatch.MaxOperations != 1 || c.ElasticsearchWriteBatch.WaitMilliseconds != 0 || c.ElasticsearchWriteBatch.ReadWaitMilliseconds != 0 || c.ElasticsearchWriteBatch.MaxConcurrentBatches != 1 {
		t.Fatal("stale derived limits")
	}
	disabled := false
	b := (ElasticsearchWriteBatchConfig{Enabled: &disabled, MaxBytes: 1 << 20}).WithDefaults(32)
	if *b.Enabled || b.MaxBytes != 1<<20 {
		t.Fatal("explicit controls lost")
	}
	*b.Enabled = true
	if disabled {
		t.Fatal("shared enabled pointer")
	}
}

func TestWriteBatchRejectsManualScheduling(t *testing.T) {
	for _, field := range []string{"max_operations", "wait_milliseconds", "read_wait_milliseconds", "max_concurrent_batches"} {
		var c LifecycleConfig
		decoder := yaml.NewDecoder(strings.NewReader("elasticsearch_write_batch:\n  " + field + ": 1\n"))
		decoder.KnownFields(true)
		if err := decoder.Decode(&c); err == nil {
			t.Fatalf("accepted manual %s", field)
		}
	}
}

func TestWriteBatchInvalidLimits(t *testing.T) {
	for _, n := range []int{-1, 1024, 17 << 20} {
		if (ElasticsearchWriteBatchConfig{MaxBytes: n}).Validate() == nil {
			t.Errorf("accepted max_bytes=%d", n)
		}
	}
}
