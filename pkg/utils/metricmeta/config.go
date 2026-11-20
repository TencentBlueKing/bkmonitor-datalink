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
	"time"
)

// ErrClosed 与 ErrUnconfigured 分别表示实例已关闭和缺少发送回调。
var (
	ErrClosed       = errors.New("metricmeta: closed")
	ErrUnconfigured = errors.New("metricmeta: export callback required")
)

// Config 配置指标元信息的去重、限流和发送。使用 DefaultConfig 初始化。
type Config struct {
	Dedup    DedupConfig    `config:"-"`          // 控制同一指标的相同元信息在多久内只上报一次。
	Exporter ExporterConfig `config:"exporter"`   // 控制待发队列和批量发送。
	Limits   Limits         `config:"rate_limit"` // 控制本实例及每个来源每分钟可接受的更新次数。
}

// DedupConfig 控制成功版本去重。
type DedupConfig struct {
	MaxAge time.Duration // 已上报版本的最长去重时间。默认 2 h，每半个周期轮换一次。
}

// ExporterConfig 控制一个实例如何缓存并发送元信息。
type ExporterConfig struct {
	Interval  time.Duration `config:"interval"`   // 每隔多久开始一轮发送。
	Timeout   time.Duration `config:"timeout"`    // 一轮发送的总时限，所有批次共用。
	BatchSize int           `config:"batch_size"` // 每个发送批次最多包含多少条元信息。
	QueueSize int           `config:"queue_size"` // 待发队列最多容纳多少个不同指标，同键更新只占一条。
}

// Limits 是固定一分钟窗口内的准入额度。
type Limits struct {
	ByDataID      map[int32]int `config:"-"`                   // 可见来源的单独额度，零表示禁止。
	Instance      int           `config:"instance_per_minute"` // 每个 collector 每分钟最多处理多少次元信息新增或变化，所有来源合计。
	DefaultSource int           `config:"dataid_per_minute"`   // 同一 collector 内，每个来源 DataID 每分钟最多处理多少次元信息新增或变化，可单独覆盖。
}

// DefaultConfig 返回默认的去重、限流和发送配置。
func DefaultConfig() Config {
	return Config{
		Exporter: ExporterConfig{Interval: time.Minute, Timeout: 5 * time.Second, BatchSize: 1000, QueueSize: 100_000},
		Dedup:    DedupConfig{MaxAge: 2 * time.Hour},
		Limits:   Limits{Instance: 100000, DefaultSource: 10000},
	}
}

func (c Config) validate() error {
	if c.Dedup.MaxAge/2 <= 0 || c.Limits.Instance <= 0 || c.Limits.DefaultSource <= 0 {
		return errors.New("metricmeta: invalid dedup or limits configuration")
	}
	for id, n := range c.Limits.ByDataID {
		if id <= 0 || n < 0 {
			return errors.New("metricmeta: invalid source limit")
		}
	}
	switch {
	case c.Exporter.Interval <= 0:
		return errors.New("metricmeta: exporter.interval must be positive")
	case c.Exporter.Timeout <= 0:
		return errors.New("metricmeta: exporter.timeout must be positive")
	case c.Exporter.BatchSize <= 0:
		return errors.New("metricmeta: exporter.batch_size must be positive")
	case c.Exporter.QueueSize <= 0:
		return errors.New("metricmeta: exporter.queue_size must be positive")
	}
	return nil
}
