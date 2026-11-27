// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestQueryFlagsDefaultsAndInheritance(t *testing.T) {
	original := viper.GetBool(DorisDirectQueryConfigPath)
	t.Cleanup(func() { viper.Set(DorisDirectQueryConfigPath, original) })
	for _, def := range []bool{false, true} {
		viper.Set(DorisDirectQueryConfigPath, def)
		for _, tc := range []struct {
			header string
			want   bool
		}{
			{"", def}, {"{}", def}, {`{"future_flag":true}`, def},
			{`{"doris_direct_query":true}`, true}, {`{"doris_direct_query":false}`, false},
		} {
			flags, err := ParseQueryFlags(tc.header)
			require.NoError(t, err)
			ctx := WithQueryFlags(context.Background(), flags)
			require.Equal(t, tc.want, GetQueryFlags(ctx).DorisDirectQuery)
			// 默认配置变化不能改变已解析请求及其子查询。
			viper.Set(DorisDirectQueryConfigPath, !def)
			child, cancel := context.WithCancel(ctx)
			require.Equal(t, tc.want, GetQueryFlags(child).DorisDirectQuery)
			cancel()
			viper.Set(DorisDirectQueryConfigPath, def)
		}
	}
}

func TestQueryFlagsInvalidHeader(t *testing.T) {
	for _, header := range []string{`null`, `[]`, `true`, `{`, `{} {}`, `{"doris_direct_query":"true"}`, `{"doris_direct_query":1}`} {
		_, err := ParseQueryFlags(header)
		require.Error(t, err, header)
	}
}

func TestSelectDorisQueryBackend(t *testing.T) {
	for _, preferDirect := range []bool{false, true} {
		for _, physical := range []bool{false, true} {
			for _, bkbase := range []bool{false, true} {
				got, err := SelectDorisQueryBackend(EffectiveQueryFlags{DorisDirectQuery: preferDirect}, physical, bkbase)
				if !physical && !bkbase {
					require.Error(t, err)
					continue
				}
				require.NoError(t, err)
				want := BkSqlStorageType
				if physical && (preferDirect || !bkbase) {
					want = DorisStorageType
				}
				require.Equal(t, want, got)
			}
		}
	}
}

func TestQueryFlagsPreserveOptionalBooleanValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    any
		enable func(context.Context) context.Context
		read   func(context.Context) bool
	}{
		{"strict", strictQueryFailuresKey{}, WithStrictQueryFailures, IsStrictQueryFailures},
		{"exact", exactTimeGridKey{}, WithExactTimeGrid, IsExactTimeGrid},
		{"left-open", leftOpenTimeWindowKey{}, WithLeftOpenTimeWindow, IsLeftOpenTimeWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.False(t, tc.read(parent))
			require.False(t, tc.read(context.WithValue(parent, tc.key, false)))
			require.True(t, tc.read(context.WithValue(parent, tc.key, true)))
			enabled := tc.enable(context.WithValue(parent, tc.key, false))
			require.True(t, tc.read(enabled))
			child := context.WithValue(enabled, namedOutputScopeContextKey{}, "output")
			require.True(t, tc.read(child))
			require.False(t, tc.read(parent))
			cancel()
			require.ErrorIs(t, child.Err(), context.Canceled)
			require.True(t, tc.read(child))
		})
	}
}
