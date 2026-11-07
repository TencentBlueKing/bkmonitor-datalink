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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestBKRUMWebVitals(t *testing.T) {
	spans := testSpans(t, testRecord("web_vitals", map[string]any{"FCP": 0, "LCP": 2501, "INP": 200, "TTFB": 801, "CLS": 0.1, "FID": 50, "aegisv2_goto": "vital-1"}))
	require.Equal(t, 5, spans.Len())
	for i, tc := range []struct {
		metric  string
		value   float64
		outcome string
	}{
		{"fcp", 0, "success"}, {"lcp", 2501, "warning"}, {"inp", 200, "success"}, {"ttfb", 801, "warning"}, {"cls", 0.1, "success"},
	} {
		span := spans.At(i)
		assert.Equal(t, "browser.web_vital", span.Name())
		assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
		assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
		assert.Equal(t, testTimestamp, span.StartTimestamp().AsTime().UnixMilli())
		assert.Equal(t, ptrace.StatusCodeUnset, span.Status().Code())
		assertAttrs(t, span.Attributes(), map[string]any{"span_type": "vital", "vital.metric": tc.metric, "vital.value": tc.value, "outcome.type": tc.outcome, "vital.id": "vital-1." + tc.metric})
		assertAbsent(t, span.Attributes(), "vital.rating", "navigationType", "span_subtype", "target_value", "result", "url.full")
	}
	unavailable := testSpans(t, testRecord("web_vitals", map[string]any{"FCP": -1, "LCP": "NaN", "INP": "+Inf", "TTFB": "-Inf", "FID": 1}))
	assert.Zero(t, unavailable.Len())
	zero := testSpans(t, testRecord("web_vitals", map[string]any{"CLS": 0})).At(0)
	id, ok := zero.Attributes().Get("vital.id")
	require.True(t, ok)
	assert.NotEmpty(t, id.Str())
}
