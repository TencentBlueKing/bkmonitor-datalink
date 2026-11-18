// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/prashantv/gostub"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// syncSample reads one config sync series from the process registry.
func syncSample(t *testing.T, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.GetMetric() {
			matched := 0
			for _, pair := range sample.GetLabel() {
				if labels[pair.GetName()] == pair.GetValue() {
					matched++
				}
			}
			if matched != len(labels) {
				continue
			}
			if sample.GetCounter() != nil {
				return sample.GetCounter().GetValue(), true
			}
			return sample.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func storageFailures(t *testing.T, reason string) float64 {
	t.Helper()
	value, _ := syncSample(t, "unify_query_config_sync_failures_total", map[string]string{"source": "storage", "reason": reason})
	return value
}

// The InfluxDB host list's watcher: a failed read of Consul takes storage down and is backend; a value
// that does not parse is decode and leaves it up, since Consul answered; a good read is a success.
func TestInfluxDBStorageWatcherSyncMetrics(t *testing.T) {
	log.InitTestLogger()
	saved := []any{StorageSource, Timeout, ContentType, PerQueryMaxGoroutine, ChunkSize, MaxLimit, MaxSLimit, Tolerance}
	defer func() {
		StorageSource, Timeout, ContentType = saved[0].(string), saved[1].(string), saved[2].(string)
		PerQueryMaxGoroutine, ChunkSize, MaxLimit = saved[3].(int), saved[4].(int), saved[5].(int)
		MaxSLimit, Tolerance = saved[6].(int), saved[7].(int)
	}()
	StorageSource, Timeout, ContentType = "consul", "30s", "application/x-msgpack"
	PerQueryMaxGoroutine, ChunkSize, MaxLimit, MaxSLimit, Tolerance = 2, 20000, 5000000, 200000, 5

	s := &Service{ctx: context.Background()}
	watching := true
	s.setStorageSync(&watching, nil)
	up := func() float64 {
		value, ok := syncSample(t, "unify_query_config_sync_up", map[string]string{"source": "storage"})
		if !ok {
			t.Fatal("storage up series missing")
		}
		return value
	}
	backend, decode := storageFailures(t, "backend"), storageFailures(t, "decode")

	stubs := gostub.StubFunc(&consul.GetDataWithPrefixContext, api.KVPairs(nil), errors.New("connection refused"))
	if err := s.reloadStorage(); err == nil {
		t.Fatal("a failed read must fail the reload")
	}
	stubs.Reset()
	if storageFailures(t, "backend") != backend+1 || up() != 0 {
		t.Fatalf("want one backend failure and storage down, got backend %v up %v", storageFailures(t, "backend")-backend, up())
	}

	stubs = gostub.StubFunc(&consul.GetDataWithPrefixContext, api.KVPairs{
		{Key: "bkmonitorv3/unify-query/data/storage/influxdb-1", Value: []byte(`{"address":`)},
	}, nil)
	if err := s.reloadStorage(); err == nil {
		t.Fatal("a value that does not parse must fail the reload")
	}
	stubs.Reset()
	if storageFailures(t, "decode") != decode+1 || storageFailures(t, "backend") != backend+1 || up() != 1 {
		t.Fatalf("want one decode failure and storage up, got decode %v backend %v up %v",
			storageFailures(t, "decode")-decode, storageFailures(t, "backend")-backend, up())
	}

	before := float64(time.Now().Unix())
	stubs = gostub.StubFunc(&consul.GetDataWithPrefixContext, api.KVPairs{
		{Key: "bkmonitorv3/unify-query/data/storage/influxdb-1", Value: []byte(`{"address":"http://influxdb.invalid:8086","type":"influxdb"}`)},
	}, nil)
	defer stubs.Reset()
	if err := s.reloadStorage(); err != nil {
		t.Fatal(err)
	}
	if at, ok := syncSample(t, "unify_query_config_sync_last_success_timestamp_seconds", map[string]string{"source": "storage"}); !ok || at < before {
		t.Fatalf("want the time of the good read, got %v %v", at, ok)
	}
	if storageFailures(t, "decode") != decode+1 || storageFailures(t, "backend") != backend+1 || up() != 1 {
		t.Fatal("a good read counts no failure and keeps storage up")
	}

	// The watch closing takes storage down even though reads succeed.
	watching = false
	s.setStorageSync(&watching, nil)
	if up() != 0 {
		t.Fatal("a closed watch takes storage down")
	}
}
