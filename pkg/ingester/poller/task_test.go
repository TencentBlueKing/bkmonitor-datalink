// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/poller"
)

func TestRegisterTask(t *testing.T) {
	d := &define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option: define.Plugin{
			PluginID:   "test",
			PluginType: "dummy_pull",
		},
	}
	poller.RegisterTask(d)
	task := poller.ListRegisteredTask()["test"]
	assert.True(t, task.IsRunning())
	poller.UnregisterTask(d)
	assert.False(t, task.IsRunning())
	assert.Empty(t, poller.ListRegisteredTask())

	assert.NoError(t, task.Once())
}

func TestRegisterBizTask(t *testing.T) {
	d := &define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option: define.Plugin{
			PluginID:   "test",
			PluginType: "dummy_pull",
			BusinessID: "123",
		},
		DataID: 10001,
	}
	d1 := &define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option: define.Plugin{
			PluginID:   "test",
			PluginType: "dummy_pull",
		},
		DataID: 10001,
	}
	d2 := &define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option: define.Plugin{
			PluginID:   "test0",
			PluginType: "dummy_pull",
			BusinessID: 0,
		},
		DataID: 10001,
	}
	poller.RegisterTask(d)
	poller.RegisterTask(d1)
	poller.RegisterTask(d2)
	task := poller.ListRegisteredTask()["test_10001"]
	task1 := poller.ListRegisteredTask()["test"]
	task2 := poller.ListRegisteredTask()["test0"]
	assert.True(t, task.IsRunning())
	assert.True(t, task1.IsRunning())
	assert.True(t, task2.IsRunning())
	poller.UnregisterTask(d)
	poller.UnregisterTask(d1)
	poller.UnregisterTask(d2)
	assert.False(t, task.IsRunning())
	assert.Empty(t, poller.ListRegisteredTask())

	assert.NoError(t, task.Once())
}
