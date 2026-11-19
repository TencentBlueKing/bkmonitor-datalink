// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

// subConfigField names, for every processor kind the collector registers, a
// field of its sub-configuration that has a concrete type, so a function
// given as its value cannot decode.
var subConfigField = map[string]string{
	define.ProcessorApdexCalculator: "calculator",
	define.ProcessorAttributeFilter: "as_string",
	define.ProcessorDbFilter:        "slow_query",
	define.ProcessorFieldNormalizer: "fields",
	define.ProcessorLicenseChecker:  "enabled",
	define.ProcessorMethodFilter:    "drop_span",
	define.ProcessorMetricsFilter:   "drop",
	define.ProcessorPprofTranslator: "type",
	define.ProcessorProbeFilter:     "add_attributes",
	define.ProcessorProxyValidator:  "type",
	define.ProcessorRateLimiter:     "type",
	define.ProcessorResourceFilter:  "drop",
	define.ProcessorSampler:         "type",
	define.ProcessorServiceDiscover: "rules",
	define.ProcessorTextSpliter:     "separator",
	define.ProcessorTokenChecker:    "type",
	define.ProcessorTracesDeriver:   "operations",
}

func subCells(t *testing.T) map[string]float64 {
	t.Helper()
	cells := map[string]float64{}
	for kind := range subConfigField {
		cells[kind], _ = metrictest.Value(t, "bk_collector_processor_config_failed_total", map[string]string{"kind": kind, "config": "sub"})
	}
	return cells
}

// Every processor kind, built with one sub-configuration that does not
// decode, still builds and counts the skipped one under its own kind and no
// other, so a factory that names another kind than its own is caught.
func TestEachProcessorCountsItsSkippedSubConfigUnderItsOwnKind(t *testing.T) {
	require.Len(t, subConfigField, 17)
	for kind, field := range subConfigField {
		create := processor.GetProcessorCreator(kind)
		require.NotNil(t, create, kind)

		before := subCells(t)
		_, err := create(map[string]any{}, []processor.SubConfigProcessor{{
			Token:  "bad-app",
			Type:   define.SubConfigFieldDefault,
			Config: processor.Config{Name: kind, Config: map[string]any{field: func() {}}},
		}})
		require.NoError(t, err, kind)

		want := map[string]float64{}
		for k, v := range before {
			want[k] = v
		}
		want[kind]++
		assert.Equal(t, want, subCells(t), "building %s", kind)
	}
}
