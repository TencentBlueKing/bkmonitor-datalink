// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestParamClean(t *testing.T) {
	param := NewBaseTaskParam()
	require.NoError(t, param.CleanParams())
	require.Equal(t, define.DefaultTimeout, param.Timeout)
	require.Equal(t, define.DefaultTimeout, param.AvailableDuration)
	require.Equal(t, define.DefaultPeriod, param.Period)
}

func TestMetaParamClean(t *testing.T) {
	param := NewBaseTaskMetaParam()
	require.NoError(t, param.CleanParams())
	require.Equal(t, define.DefaultTimeout, param.MaxTimeout)
	require.Equal(t, define.DefaultPeriod, param.MinPeriod)
}

func TestBaseTaskParamGetBizID(t *testing.T) {
	t.Run("fallback to config biz id", func(t *testing.T) {
		param := BaseTaskParam{BizID: 2}
		require.NoError(t, param.CleanParams())
		require.Equal(t, int32(2), param.GetBizID())
	})

	t.Run("use label biz id override", func(t *testing.T) {
		param := BaseTaskParam{
			BizID: 2,
			Labels: []map[string]string{{
				labelBizID: "5",
			}},
		}
		require.NoError(t, param.CleanParams())
		require.Equal(t, int32(5), param.GetBizID())
	})

	t.Run("allow same biz id across label groups", func(t *testing.T) {
		param := BaseTaskParam{
			BizID: 2,
			Labels: []map[string]string{
				{labelBizID: "5"},
				{labelBizID: "5", "bk_target_ip": "10.0.0.1"},
			},
		}
		require.NoError(t, param.CleanParams())
		require.Equal(t, int32(5), param.GetBizID())
	})

	t.Run("reject invalid label biz id", func(t *testing.T) {
		param := BaseTaskParam{
			BizID: 2,
			Labels: []map[string]string{{
				labelBizID: "invalid",
			}},
		}
		require.Error(t, param.CleanParams())
	})

	t.Run("reject conflicting label biz id", func(t *testing.T) {
		param := BaseTaskParam{
			BizID: 2,
			Labels: []map[string]string{
				{labelBizID: "5"},
				{labelBizID: "8"},
			},
		}
		require.Error(t, param.CleanParams())
	})

	t.Run("net task delegates biz id resolution", func(t *testing.T) {
		param := NetTaskParam{
			BaseTaskParam: BaseTaskParam{
				BizID: 2,
				Labels: []map[string]string{{
					labelBizID: "5",
				}},
			},
		}
		require.NoError(t, param.CleanParams())
		require.Equal(t, int32(5), param.GetBizID())
	})
}
