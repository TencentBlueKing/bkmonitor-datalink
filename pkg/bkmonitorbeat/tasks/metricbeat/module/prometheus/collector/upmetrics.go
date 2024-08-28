// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"bytes"
	"fmt"
	"io"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

var innerMetrics = map[string]struct{}{
	define.NameMetricBeatUp:             {},
	define.NameMetricBeatScrapeDuration: {},
	define.NameMetricBeatScrapeSize:     {},
	define.NameMetricBeatScrapeLine:     {},
	define.NameMetricBeatHandleDuration: {},
}

func IsInnerMetric(s string) bool {
	_, ok := innerMetrics[s]
	return ok
}

func NewCodeReader(code define.NamedCode, kvs []define.LogKV) io.ReadCloser {
	r := bytes.NewReader([]byte(CodeUp(code, kvs)))
	return io.NopCloser(r)
}

const (
	prefixMetricbeat = "[metricbeat] "
)

func CodeUp(code define.NamedCode, kvs []define.LogKV) string {
	s := fmt.Sprintf(`%s{code="%d",code_name="%s"} 1`, define.NameMetricBeatUp, code.Code(), code.Name())
	define.RecordLog(prefixMetricbeat+s, kvs)
	return s
}

func CodeScrapeDuration(seconds float64, kvs []define.LogKV) string {
	s := fmt.Sprintf(`%s{} %f`, define.NameMetricBeatScrapeDuration, seconds)
	define.RecordLog(prefixMetricbeat+s, kvs)
	return s
}

func CodeScrapeSize(size int, kvs []define.LogKV) string {
	s := fmt.Sprintf(`%s{} %d`, define.NameMetricBeatScrapeSize, size)
	define.RecordLog(prefixMetricbeat+s, kvs)
	return s
}

func CodeScrapeLine(n int, kvs []define.LogKV) string {
	s := fmt.Sprintf(`%s{} %d`, define.NameMetricBeatScrapeLine, n)
	define.RecordLog(prefixMetricbeat+s, kvs)
	return s
}

func CodeHandleDuration(seconds float64, kvs []define.LogKV) string {
	s := fmt.Sprintf(`%s{} %f`, define.NameMetricBeatHandleDuration, seconds)
	define.RecordLog(prefixMetricbeat+s, kvs)
	return s
}
