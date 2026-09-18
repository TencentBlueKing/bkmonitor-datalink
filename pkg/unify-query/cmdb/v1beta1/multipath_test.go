// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
)

func TestModel_QueryResourceMatcherAll(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid, SkipSpace: "skip"})

	mock.Vm.Set(map[string]any{
		"query:1693973987count by (bk_target_ip) (a)": victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{{
				Metric: map[string]string{"bk_target_ip": "127.0.0.1"},
				Value:  []any{1693973987, "1"},
			}},
		},
	})

	source, sourceInfo, paths, target, err := testModel.QueryResourceMatcherAll(
		ctx,
		"",
		influxdb.SpaceUid,
		"1693973987",
		"system",
		"node",
		cmdb.Matcher{
			"bcs_cluster_id": "BCS-K8S-00000",
			"node":           "node-127-0-0-1",
		},
		nil,
		false,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, cmdb.Resource("node"), source)
	assert.Equal(t, cmdb.Matcher{
		"bcs_cluster_id": "BCS-K8S-00000",
		"node":           "node-127-0-0-1",
	}, sourceInfo)
	assert.Equal(t, cmdb.Resource("system"), target)
	require.NotEmpty(t, paths)
	assert.Equal(t, []string{"node", "system"}, paths[0].Path)
	assert.Equal(t, cmdb.Matchers{{"bk_target_ip": "127.0.0.1"}}, paths[0].TargetList)

	// 未命中的候选路径也被保留，调用方可以观察到完整的静态路径集合。
	assert.GreaterOrEqual(t, len(paths), 2)
	for _, path := range paths {
		assert.NotNil(t, path.TargetList)
	}
}

func TestModel_QueryResourceMatcherAllReturnsErrorWhenAllPathsFail(t *testing.T) {
	mock.Init()
	mock.Vm.Clear()

	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid, SkipSpace: "skip"})

	_, _, paths, _, err := testModel.QueryResourceMatcherAll(
		ctx,
		"",
		influxdb.SpaceUid,
		"1693973987",
		"system",
		"node",
		cmdb.Matcher{
			"bcs_cluster_id": "BCS-K8S-00000",
			"node":           "node-127-0-0-1",
		},
		nil,
		false,
		[]cmdb.Resource{"node", "system"},
	)

	assert.Nil(t, paths)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all relation paths failed")
}
