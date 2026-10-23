// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package rum

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestDefaultsPreserveKnownValuesAndMissingMeasurements(t *testing.T) {
	resource := pcommon.NewMap()
	ApplyResourceDefaults(resource)
	assert.Equal(t, map[string]any{ServiceNameKey: DefaultServiceName}, resource.AsRaw())
	resource.PutString(ServiceNameKey, "checkout")
	ApplyResourceDefaults(resource)
	assert.Equal(t, "checkout", resource.AsRaw()[ServiceNameKey])

	span := testSpan(Resource, "")
	attrs := span.Attributes()
	ApplySpanDefaults(span)
	assert.Equal(t, "other", attrs.AsRaw()[ResourceTypeKey])
	_, hasSession := attrs.Get(SessionTypeKey)
	assert.False(t, hasSession)
	attrs.PutString(SessionIDKey, "session-1")
	ApplySpanDefaults(span)
	assert.Equal(t, "user", attrs.AsRaw()[SessionTypeKey])

	attrs.PutString(ResourceTypeKey, "xhr")
	attrs.PutString(SessionTypeKey, "synthetics")
	attrs.PutString(OutcomeTypeKey, "timeout")
	attrs.PutInt("resource.transfer_size", 0)
	attrs.PutBool("session.has_replay", false)
	before := attrs.AsRaw()
	ApplySpanDefaults(span)
	assert.Equal(t, before, attrs.AsRaw())
	for _, key := range []string{"resource.size", "resource.dns.duration", "resource.cache.hit", "view.version", "view.started_at", "session.sample_rate"} {
		_, ok := attrs.Get(key)
		assert.False(t, ok, "must not fabricate %s", key)
	}

	errorSpan := testSpan(Error, "")
	ApplySpanDefaults(errorSpan)
	assert.Equal(t, false, errorSpan.Attributes().AsRaw()[ErrorHandledKey])
	errorSpan.Attributes().PutBool(ErrorHandledKey, true)
	ApplySpanDefaults(errorSpan)
	assert.Equal(t, true, errorSpan.Attributes().AsRaw()[ErrorHandledKey])
}

func TestVitalOutcomeBoundaries(t *testing.T) {
	for metric, boundary := range map[string]float64{"fcp": 1800, "lcp": 2500, "inp": 200, "ttfb": 800, "cls": 0.1} {
		for _, value := range []float64{0, boundary} {
			outcome, ok := VitalOutcome(metric, value)
			require.True(t, ok, metric)
			assert.Equal(t, "success", outcome)
		}
		outcome, ok := VitalOutcome(metric, math.Nextafter(boundary, math.Inf(1)))
		require.True(t, ok, metric)
		assert.Equal(t, "warning", outcome)
		for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
			_, ok := VitalOutcome(metric, invalid)
			assert.False(t, ok, metric)
		}
	}
	for _, metric := range []string{"page_performance.ttfb", "page_performance.firstScreenTiming"} {
		outcome, ok := VitalOutcome(metric, 9000)
		require.True(t, ok)
		assert.Equal(t, "success", outcome)
		for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
			_, ok := VitalOutcome(metric, invalid)
			assert.False(t, ok)
		}
	}
	for _, metric := range []string{"", "  "} {
		_, ok := VitalOutcome(metric, 0)
		assert.False(t, ok)
	}
}

func TestValidateVital(t *testing.T) {
	span := testSpan(Vital, "")
	ApplySpanDefaults(span)
	require.Error(t, ValidateSpan(span))
	attrs := span.Attributes()
	attrs.PutString("vital.metric", "cls")
	attrs.PutString("vital.id", "vital-1")
	attrs.PutInt("vital.value", 0)
	require.NoError(t, ValidateSpan(span))
	attrs.PutDouble("vital.value", 0.3)
	require.NoError(t, ValidateSpan(span))
	attrs.PutString("vital.value", "0.3")
	require.Error(t, ValidateSpan(span))
	attrs.PutDouble("vital.value", math.NaN())
	require.Error(t, ValidateSpan(span))
	attrs.PutDouble("vital.value", 0)
	attrs.PutString("vital.metric", "page_performance.firstScreenTiming")
	require.NoError(t, ValidateSpan(span))
	attrs.PutString("vital.metric", "")
	require.Error(t, ValidateSpan(span))
	attrs.PutInt("vital.metric", 123)
	require.Error(t, ValidateSpan(span))
	attrs.PutString("vital.metric", "page_performance.firstScreenTiming")
	attrs.PutString("vital.id", " ")
	require.Error(t, ValidateSpan(span))
}
