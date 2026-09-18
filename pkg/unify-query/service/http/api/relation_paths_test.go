// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestSelectLegacyInstantPath(t *testing.T) {
	paths := []cmdb.RelationMultiResourcePathData{
		{Path: []string{"node", "pod"}, TargetList: cmdb.Matchers{}},
		{Path: []string{"node", "replicaset", "pod"}, TargetList: cmdb.Matchers{{"pod": "pod-1"}}},
		{Path: []string{"node", "deployment", "pod"}, TargetList: cmdb.Matchers{{"pod": "pod-2"}}},
	}

	path, targetList := selectLegacyInstantPath(paths)
	assert.Equal(t, []string{"node", "replicaset", "pod"}, path)
	assert.Equal(t, cmdb.Matchers{{"pod": "pod-1"}}, targetList)
}

func TestSelectLegacyInstantPathWhenAllEmpty(t *testing.T) {
	paths := []cmdb.RelationMultiResourcePathData{
		{Path: []string{"node", "pod"}, TargetList: cmdb.Matchers{}},
		{Path: []string{"node", "replicaset", "pod"}, TargetList: cmdb.Matchers{}},
	}

	path, targetList := selectLegacyInstantPath(paths)
	assert.Equal(t, []string{"node", "replicaset", "pod"}, path)
	assert.NotNil(t, targetList)
	assert.Empty(t, targetList)
}

func TestSelectLegacyRangePath(t *testing.T) {
	paths := []cmdb.RelationMultiResourceRangePathData{
		{Path: []string{"node", "pod"}, TargetList: []cmdb.MatchersWithTimestamp{}},
		{Path: []string{"node", "replicaset", "pod"}, TargetList: []cmdb.MatchersWithTimestamp{{Timestamp: 1}}},
	}

	path, targetList := selectLegacyRangePath(paths)
	assert.Equal(t, []string{"node", "replicaset", "pod"}, path)
	assert.Equal(t, []cmdb.MatchersWithTimestamp{{Timestamp: 1}}, targetList)
}
