// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"errors"
	"math"
	"time"
)

var (
	ErrClosed       = errors.New("metricmeta: closed")
	ErrUnconfigured = errors.New("metricmeta: export callback required")
)

type Config struct {
	FlushInterval       time.Duration `config:"flush_interval"`
	FullReportInterval  time.Duration `config:"full_report_interval"`
	BloomCapacity       int           `config:"bloom_capacity"`
	BloomErrorRate      float64       `config:"bloom_error_rate"`
	MaxPendingEntries   int           `config:"max_pending_entries"`
	MaxEntryBytes       int           `config:"max_entry_bytes"`
	ExportBatchSize     int           `config:"export_batch_size"`
	ExportBatchBytes    int           `config:"export_batch_bytes"`
	ExportMinInterval   time.Duration `config:"export_min_interval"`
	ExportTimeout       time.Duration `config:"export_timeout"`
	DebounceInterval    time.Duration `config:"debounce_interval"`
	DebounceMaxWait     time.Duration `config:"debounce_max_wait"`
	CircuitWindow       time.Duration `config:"circuit_window"`
	MaxUpdates          int           `config:"max_updates"`
	MaxUpdatesPerDataID int           `config:"max_updates_per_dataid"`
	DataIDLimits        []DataIDLimit `config:"dataid_limits"`
	BlockedDataIDs      []int32       `config:"blocked_dataids"`
}

type DataIDLimit struct {
	DataID int32 `config:"dataid"`
	Limit  int   `config:"max_updates"`
}

func DefaultConfig() Config {
	return Config{
		FlushInterval: time.Minute, FullReportInterval: 2 * time.Hour,
		BloomCapacity: 100000, BloomErrorRate: 1e-6,
		MaxPendingEntries: 100000, MaxEntryBytes: 4096,
		ExportBatchSize: 1000, ExportBatchBytes: 256 * 1024,
		ExportMinInterval: 100 * time.Millisecond, ExportTimeout: 5 * time.Second,
		DebounceInterval: time.Minute, DebounceMaxWait: 10 * time.Minute,
		CircuitWindow: time.Minute, MaxUpdates: 100000, MaxUpdatesPerDataID: 10000,
	}
}

func (c Config) validate() error {
	if c.FlushInterval <= 0 || c.FullReportInterval/2 <= c.FlushInterval ||
		c.BloomCapacity <= 0 || math.IsNaN(c.BloomErrorRate) || c.BloomErrorRate <= 0 || c.BloomErrorRate >= 1 ||
		c.MaxPendingEntries <= 0 || c.MaxEntryBytes <= 0 || c.ExportBatchSize <= 0 || c.ExportBatchSize > c.MaxPendingEntries ||
		c.ExportBatchBytes < 2 || c.ExportMinInterval <= 0 || c.ExportTimeout <= c.ExportMinInterval || c.ExportTimeout >= c.FlushInterval ||
		c.DebounceInterval < 0 || c.DebounceMaxWait < c.DebounceInterval || c.DebounceMaxWait <= 0 ||
		c.CircuitWindow <= 0 || c.MaxUpdates <= 0 || c.MaxUpdatesPerDataID <= 0 {
		return errors.New("metricmeta: invalid configuration")
	}
	seen := make(map[int32]bool, len(c.DataIDLimits))
	for _, item := range c.DataIDLimits {
		if item.DataID <= 0 || item.Limit < 0 || seen[item.DataID] {
			return errors.New("metricmeta: invalid dataid limit")
		}
		seen[item.DataID] = true
	}
	for _, id := range c.BlockedDataIDs {
		if id <= 0 {
			return errors.New("metricmeta: invalid blocked dataid")
		}
	}
	// 每个过滤器最多 512 MiB，避免错误配置导致整数溢出或无界分配。
	bits := -float64(c.BloomCapacity) * math.Log(c.BloomErrorRate/2) / (math.Ln2 * math.Ln2)
	if math.IsInf(bits, 0) || bits > 512*1024*1024*8 {
		return errors.New("metricmeta: bloom capacity too large")
	}
	return nil
}
