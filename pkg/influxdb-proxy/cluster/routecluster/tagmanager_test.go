// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package routecluster_test

import (
	"context"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster/routecluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

type TestTagManagerSuite struct {
	suite.Suite
	ctrl *gomock.Controller
	stub *gostub.Stubs
}

func (t *TestTagManagerSuite) SetupSuite() {
	t.ctrl = gomock.NewController(t.T())
	t.stub = gostub.New()
}

func (t *TestTagManagerSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stub.Reset()
}

// 测试根据tag获取backend的基础逻辑
func (t *TestTagManagerSuite) TestGetBackends() {
	backend1 := NewProxyBackend(t.ctrl)
	backend1.EXPECT().Name().Return("backend1").AnyTimes()
	backend2 := NewProxyBackend(t.ctrl)
	backend2.EXPECT().Name().Return("backend2").AnyTimes()
	backend3 := NewProxyBackend(t.ctrl)
	backend3.EXPECT().Name().Return("backend3").AnyTimes()
	backend4 := NewProxyBackend(t.ctrl)
	backend4.EXPECT().Name().Return("backend4").AnyTimes()
	backendList := []backend.Backend{
		backend1, backend2, backend3, backend4,
	}
	manager := routecluster.NewTagInfoManager(context.Background(), "test_cluster", 2, backendList)
	tagInfos := map[string]*consul.TagInfo{
		"test1": {
			HostList: []string{"backend1", "backend2"},
		},
		"test2": {
			HostList: []string{"backend2", "backend3"},
		},
		"test3": {
			HostList:       []string{"backend3", "backend4"},
			UnreadableHost: []string{"backend1", "backend2"},
		},
	}
	t.stub.StubFunc(&consul.GetTagsInfo, tagInfos, nil)

	searchList := []string{"test1", "test2", "test3"}
	expectedReadResults := [][]string{
		{"backend1", "backend2"},
		{"backend2", "backend3"},
		{"backend3", "backend4"},
	}
	expectedWriteResults := [][]string{
		{"backend1", "backend2"},
		{"backend2", "backend3"},
		{"backend3", "backend4", "backend1", "backend2"},
	}

	t.Nil(manager.Refresh())

	for index, searchItem := range searchList {
		readBackendList, err := manager.GetReadBackends(searchItem)
		t.Nil(err)
		writeBackendList, err := manager.GetWriteBackends(searchItem)
		t.Nil(err)
		// 检查长度
		t.Equal(len(expectedReadResults[index]), len(readBackendList))
		t.Equal(len(expectedWriteResults[index]), len(writeBackendList))

		for idx, readBackend := range readBackendList {
			t.Equal(readBackend.Name(), expectedReadResults[index][idx])
		}
		for idx, writeBackend := range writeBackendList {
			t.Equal(writeBackend.Name(), expectedWriteResults[index][idx])
		}
	}
}
