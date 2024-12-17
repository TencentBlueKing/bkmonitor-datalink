// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataidwatcher

import (
	"testing"

	"github.com/stretchr/testify/assert"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

func TestMetricDataIDMatcher(t *testing.T) {
	watcher := &dataIDWatcher{}
	watcher.metricDataIDs = map[string]*bkv1beta1.DataID{
		watcher.uniqueKey("servicemonitor", "ns1", "name1"): {
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1001,
				MonitorResource: bkv1beta1.MonitorResource{
					NameSpace: "ns1",
					Name:      "name1",
					Kind:      "servicemonitor",
				},
			},
		},
		watcher.uniqueKey("servicemonitor", "ns2", ""): {
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1002,
				MonitorResource: bkv1beta1.MonitorResource{
					NameSpace: "ns2",
					Kind:      "servicemonitor",
				},
			},
		},
		watcher.uniqueKey("servicemonitor", "ns5|ns6|ns7", ""): {
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1101,
				MonitorResource: bkv1beta1.MonitorResource{
					NameSpace: "ns5|ns6|ns7",
					Kind:      "servicemonitor",
				},
			},
		},
		watcher.uniqueKey("servicemonitor", "ns7|ns8|ns9", "name3|name4"): {
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1005,
				MonitorResource: bkv1beta1.MonitorResource{
					NameSpace: "ns7|ns8|ns9",
					Name:      "name3|name4",
					Kind:      "servicemonitor",
				},
			},
		},
		defaultSystemDataIDKey: {
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1003,
			},
		},
		defaultCommonDataIDKey: {
			Spec: bkv1beta1.DataIDSpec{
				DataID: 1004,
			},
		},
	}

	t.Run("精准匹配", func(t *testing.T) {
		dataID, err := watcher.MatchMetricDataID(define.MonitorMeta{
			Name:      "name1",
			Kind:      "servicemonitor",
			Namespace: "ns1",
		}, false)
		assert.NoError(t, err)
		assert.Equal(t, 1001, dataID.Spec.DataID)
	})

	t.Run("系统内置", func(t *testing.T) {
		dataID, err := watcher.MatchMetricDataID(define.MonitorMeta{
			Name:      "name2",
			Kind:      "servicemonitor",
			Namespace: "ns1",
		}, true)
		assert.NoError(t, err)
		assert.Equal(t, 1003, dataID.Spec.DataID)
	})

	t.Run("namespace 精确匹配", func(t *testing.T) {
		dataID, err := watcher.MatchMetricDataID(define.MonitorMeta{
			Name:      "name3",
			Kind:      "servicemonitor",
			Namespace: "ns2",
		}, false)
		assert.NoError(t, err)
		assert.Equal(t, 1002, dataID.Spec.DataID)
	})

	t.Run("namespace 分割匹配", func(t *testing.T) {
		dataID, err := watcher.MatchMetricDataID(define.MonitorMeta{
			Name:      "",
			Kind:      "servicemonitor",
			Namespace: "ns5",
		}, false)
		assert.NoError(t, err)
		assert.Equal(t, 1101, dataID.Spec.DataID)
	})

	t.Run("name 分割匹配", func(t *testing.T) {
		dataID, err := watcher.MatchMetricDataID(define.MonitorMeta{
			Name:      "name3",
			Kind:      "servicemonitor",
			Namespace: "ns9",
		}, false)
		assert.NoError(t, err)
		assert.Equal(t, 1005, dataID.Spec.DataID)
	})

	t.Run("兜底匹配", func(t *testing.T) {
		dataID, err := watcher.MatchMetricDataID(define.MonitorMeta{
			Name:      "name4",
			Kind:      "servicemonitor",
			Namespace: "ns3",
		}, false)
		assert.NoError(t, err)
		assert.Equal(t, 1004, dataID.Spec.DataID)
	})
}
