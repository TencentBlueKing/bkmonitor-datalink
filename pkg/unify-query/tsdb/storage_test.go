// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"strings"
	"testing"
	"time"
)

func TestStorageConfigHashIncludesOptions(t *testing.T) {
	storages := map[string]any{
		"1": map[string]string{"type": "influxdb", "address": "http://influx"},
	}
	first := &Options{InfluxDB: &InfluxDBOption{Timeout: time.Second}}
	second := &Options{InfluxDB: &InfluxDBOption{Timeout: 2 * time.Second}}

	if StorageConfigHash(storages, first) == StorageConfigHash(storages, second) {
		t.Fatal("query option changes must alter the storage configuration hash")
	}
}

func TestPrintRedactsPassword(t *testing.T) {
	storageLock.Lock()
	previous := storageMap
	storageMap = map[string]*Storage{
		"1": {
			Type:     "influxdb",
			Address:  "http://influx",
			Username: "admin",
			Password: "plain-secret",
		},
	}
	storageLock.Unlock()
	t.Cleanup(func() {
		storageLock.Lock()
		storageMap = previous
		storageLock.Unlock()
	})

	output := Print()
	if strings.Contains(output, "plain-secret") {
		t.Fatal("storage diagnostics must not contain plaintext passwords")
	}
	if !strings.Contains(output, "password=******") {
		t.Fatal("storage diagnostics should show that the password was redacted")
	}
}
