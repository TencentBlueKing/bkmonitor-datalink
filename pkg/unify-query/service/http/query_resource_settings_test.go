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
	"context"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestQueryResourceSettingsDefaultDisabledAndReloadSafe(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Cleanup(func() { queryResourceSettingsSnapshot.Store(defaultQueryResourceSettings()) })
	t.Cleanup(func() { queryProcessBudgetSnapshot.Store(nil) })
	setDefaultConfig()
	loadQueryResourceSettings()
	require.Equal(t, *defaultQueryResourceSettings(), getQueryResourceSettings())
	require.Nil(t, queryProcessBudgetSnapshot.Load())

	viper.Set(QueryResourceEnabledConfigPath, true)
	viper.Set(QueryResourceMaxSeriesConfigPath, 100)
	viper.Set(QueryResourceMaxPointsConfigPath, 200)
	viper.Set(QueryResourceMaxBytesConfigPath, 300)
	viper.Set(QueryResourceMaxResponseBytesConfigPath, 400)
	viper.Set(QueryResourceMaxEvalCapacityBytesConfigPath, 500)
	viper.Set(QueryResourceProcessCapacityBytesConfigPath, 600)
	loadQueryResourceSettings()
	require.Equal(t, queryResourceSettings{
		Enabled:              true,
		MaxSeries:            100,
		MaxPoints:            200,
		MaxBytes:             300,
		MaxResponseBytes:     400,
		MaxEvalCapacityBytes: 500,
		ProcessCapacityBytes: 600,
	}, getQueryResourceSettings())
	processBudget := queryProcessBudgetSnapshot.Load()
	require.Equal(t, int64(600), processBudget.Capacity())

	viper.Set(QueryResourceProcessCapacityBytesConfigPath, 700)
	loadQueryResourceSettings()
	require.Same(t, processBudget, queryProcessBudgetSnapshot.Load())
	require.Equal(t, int64(700), processBudget.Capacity())
}

func TestQueryResourceSettingsRejectNegativeValues(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Cleanup(func() { queryResourceSettingsSnapshot.Store(defaultQueryResourceSettings()) })
	t.Cleanup(func() { queryProcessBudgetSnapshot.Store(nil) })
	originalWarn := warnQueryResourceConfig
	warnings := make(map[string]int)
	warnQueryResourceConfig = func(path string) { warnings[path]++ }
	t.Cleanup(func() { warnQueryResourceConfig = originalWarn })
	setDefaultConfig()

	viper.Set(QueryResourceMaxSeriesConfigPath, -1)
	loadQueryResourceSettings()
	require.Equal(t, DefaultQueryResourceMaxSeries, getQueryResourceSettings().MaxSeries)
	require.Equal(t, 1, warnings[QueryResourceMaxSeriesConfigPath])
}

func TestQueryResourceBudgetErrorWinsOverCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	budget := metadata.NewResourceBudget(metadata.ResourceBudgetLimits{MaxSeries: 1}, cancel)
	require.Error(t, budget.Reserve(2, 0, 0))

	err := queryResourceBudgetError(budget, ctx.Err())
	var limitErr *metadata.ResourceBudgetError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, metadata.ResourceSeries, limitErr.Resource)
}
