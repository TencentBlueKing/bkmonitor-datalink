// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestNamedOutputSettingsUseContractDefaultsAndRejectNonPositiveValues(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	originalWarn := warnNamedOutputConfig
	warnings := make(map[string]int)
	warnNamedOutputConfig = func(path string) { warnings[path]++ }
	t.Cleanup(func() { warnNamedOutputConfig = originalWarn })
	setDefaultConfig()
	loadNamedOutputSettings()
	require.Equal(t, defaultNamedOutputSettings(), getNamedOutputSettings())

	for _, path := range []string{
		NamedOutputsMaxOutputsConfigPath,
		NamedOutputsTimeoutConfigPath,
		NamedOutputsMaxSeriesConfigPath,
		NamedOutputsMaxPointsConfigPath,
		NamedOutputsMaxCacheBytesConfigPath,
		NamedOutputsMaxResponseBytesConfigPath,
	} {
		viper.Set(path, 0)
	}
	loadNamedOutputSettings()
	settings := getNamedOutputSettings()
	require.Equal(t, 4, settings.MaxOutputs)
	require.Equal(t, 30*time.Second, settings.Timeout)
	require.Equal(t, 10000, settings.MaxSeries)
	require.Equal(t, 1000000, settings.MaxPoints)
	require.Equal(t, int64(64*1024*1024), settings.MaxCacheBytes)
	require.Equal(t, 16*1024*1024, settings.MaxResponseBytes)
	for _, path := range []string{
		NamedOutputsMaxOutputsConfigPath,
		NamedOutputsTimeoutConfigPath,
		NamedOutputsMaxSeriesConfigPath,
		NamedOutputsMaxPointsConfigPath,
		NamedOutputsMaxCacheBytesConfigPath,
		NamedOutputsMaxResponseBytesConfigPath,
	} {
		require.Equal(t, 1, warnings[path], path)
	}
}
