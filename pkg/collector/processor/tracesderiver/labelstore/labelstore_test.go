// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package labelstore

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func TestStorageMulti(t *testing.T) {
	tests := []struct {
		h   uint64
		lbs map[string]string
	}{
		{
			h: 1,
			lbs: map[string]string{
				"status_code":            "2",
				"kind":                   "2",
				"service.name":           "service-name-1",
				"service.version":        "service-version-1",
				"telemetry.sdk.name":     "telemetry-sdk-name-1",
				"telemetry.sdk.version":  "telemetry-sdk-version-1",
				"telemetry.sdk.language": "telemetry-sdk-language-1",
			},
		},
		{
			h: 2,
			lbs: map[string]string{
				"status_code":            "1",
				"kind":                   "1",
				"service.name":           "service-name-2",
				"service.version":        "service-version-2",
				"telemetry.sdk.name":     "telemetry-sdk-name-2",
				"telemetry.sdk.version":  "telemetry-sdk-version-2",
				"telemetry.sdk.language": "telemetry-sdk-language-2",
			},
		},
	}

	storage := New()
	for _, tt := range tests {
		storage.SetIf(tt.h, tt.lbs)
	}

	for _, tt := range tests {
		v, ok := storage.Get(tt.h)
		assert.True(t, ok)
		assert.Equal(t, tt.lbs, v)
	}

	for _, tt := range tests {
		assert.True(t, storage.Exist(tt.h))
	}
	assert.False(t, storage.Exist(3))

	storage.Del(1)
	assert.False(t, storage.Exist(1))
	assert.Len(t, storage.keys, 7)

	storage.Clean()
	assert.Len(t, storage.keys, 0)
}

const (
	setCount = 100000 // 10w
	appCount = 10
)

var keys = []string{
	"resource.bk.instance.id",
	"span_name",
	"kind",
	"status.code",
	"resource.service.name",
	"resource.service.version",
	"resource.telemetry.sdk.name",
	"resource.telemetry.sdk.version",
	"resource.telemetry.sdk.language",
	"attributes.peer.service",
	"attributes.http.method",
	"attributes.http.status_code",
}

func testStorageSetIf(storage *Storage) {
	for i := 0; i < setCount; i++ {
		lbs := make(map[string]string)
		for _, k := range keys {
			lbs[k] = random.FastString(6)
		}
		storage.SetIf(uint64(i), lbs)
	}
}

func TestStorageSetIf(t *testing.T) {
	start := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < appCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			testStorageSetIf(New())
		}()
	}
	wg.Wait()
	prettyprint.RuntimeMemStats(t.Logf)
	t.Logf("Storage SetIf operation take: %v\n", time.Since(start))
}

func testStorageDel(storage *Storage) {
	for i := 0; i < setCount; i++ {
		storage.Del(uint64(i))
	}
}

func TestStorageDel(t *testing.T) {
	var storages []*Storage
	for i := 0; i < appCount; i++ {
		storage := New()
		storages = append(storages, storage)
		testStorageSetIf(storage)
	}
	start := time.Now()
	for i := 0; i < appCount; i++ {
		testStorageDel(storages[i])
	}
	prettyprint.RuntimeMemStats(t.Logf)
	t.Logf("Storage Del operation take: %v\n", time.Since(start))
}

func testStorageGet(storage *Storage) {
	for i := 0; i < setCount; i++ {
		storage.Get(uint64(i))
	}
}

func TestStorageGet(t *testing.T) {
	var storages []*Storage
	for i := 0; i < appCount; i++ {
		storage := New()
		testStorageSetIf(storage)
		storages = append(storages, storage)
	}
	start := time.Now()
	for i := 0; i < appCount; i++ {
		testStorageGet(storages[i])
	}
	prettyprint.RuntimeMemStats(t.Logf)
	t.Logf("Storage Get operation take: %v\n", time.Since(start))
}
