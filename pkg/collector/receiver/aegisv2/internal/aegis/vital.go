// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// 标准 Web Vitals 保留五项指标的名称和评级规则。
var webVitalsKeys = []struct{ metric, key string }{
	{"fcp", "FCP"}, {"lcp", "LCP"}, {"inp", "INP"}, {"ttfb", "TTFB"}, {"cls", "CLS"},
}

func (c *spanConverter) appendVitals(e aegisEvent) {
	for _, def := range webVitalsKeys {
		c.appendVital(e, def.key, def.metric)
	}
}

func (c *spanConverter) appendVital(e aegisEvent, key, metric string) {
	value, ok := extractFloat64(e.msg.raw, key)
	if !ok {
		return
	}
	outcome, valid := rum.VitalOutcome(metric, value)
	if !valid {
		return
	}
	span := c.newSpan(e.record, e.msg.Timestamp, rum.Vital, "")
	attrs := span.Attributes()
	attrs.PutString(rum.OutcomeTypeKey, outcome)
	attrs.PutString(attrVitalMetric, metric)
	attrs.PutDouble(attrVitalValue, value)
	id := firstNonEmptyString(e.msg.raw, "aegisv2_goto")
	if id == "" {
		id = span.SpanID().HexString()
	}
	attrs.PutString(attrVitalID, id+"."+metric)
}
