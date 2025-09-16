// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tracesderiver/serieslimiter"
)

const (
	ExtractorType = "duration"
)

type Extractor struct {
	limiter *serieslimiter.Limiter
}

func NewExtractor(conf *ExtractorConfig) *Extractor {
	if conf.MaxSeries <= 0 {
		conf.MaxSeries = 100000 // 100k
	}
	if conf.GcInterval <= 0 {
		conf.GcInterval = time.Hour
	}
	return &Extractor{
		limiter: serieslimiter.New(conf.MaxSeries, conf.GcInterval),
	}
}

func (e *Extractor) Extract(span ptrace.Span) float64 {
	return utils.CalcSpanDuration(span)
}

func (e *Extractor) Set(dataID int32, hash uint64) bool {
	return e.limiter.Set(dataID, hash)
}

func (e *Extractor) Stop() {
	if e.limiter != nil {
		e.limiter.Stop()
	}
}
