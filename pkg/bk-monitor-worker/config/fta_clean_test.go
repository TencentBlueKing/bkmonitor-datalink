// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"runtime"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestFTACleanConfig(t *testing.T) {
	previous, oldKeys := FTAClean, keys
	defer func() { FTAClean = previous; keys = oldKeys; viper.Reset() }()
	cpu := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(cpu)
	keys = nil
	loadFTAClean()
	require.Equal(t, 8, FTAClean.MaxConcurrency)
	require.Equal(t, 4, FTAClean.Shards)
	require.False(t, FTACleanOwns(FTAEvent, 42))
	for _, limit := range []int{1, 3, 8} {
		viper.Set("fta_clean.max_concurrency", limit)
		keys = viper.AllKeys()
		loadFTAClean()
		require.Equal(t, limit, FTAClean.MaxConcurrency)
	}
	FTAClean.Enabled = true
	FTAClean.DataIDs = []int{42}
	require.True(t, FTACleanOwns(FTAEvent, 42))
	require.False(t, FTACleanOwns(FTAEvent, 43))
	FTAClean.All = true
	require.True(t, FTACleanOwns(FTAEvent, 43))
	require.False(t, FTACleanOwns("custom", 42))
}
