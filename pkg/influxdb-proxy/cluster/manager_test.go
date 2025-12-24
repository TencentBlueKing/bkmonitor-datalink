// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster_test

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster/routecluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

func TestRun(t *testing.T) {
	suite.Run(t, &TestSuite{})
}

type TestSuite struct {
	suite.Suite
	ctrl *gomock.Controller
	stub *gostub.Stubs
}

func (t *TestSuite) SetupSuite() {
	t.ctrl = gomock.NewController(t.T())
	t.stub = gostub.New()
	// 模仿配置文件填充viper
	viper.Set("consul.address", "127.0.0.1:8500")
	viper.Set("kafka.address", "127.0.0.1")
	viper.Set("kafka.port", "9092")
	viper.Set("kafka.topic_prefix", "bkmonitor")
	viper.Set("kafka.version", "0.10.2.0")
}

func (t *TestSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stub.Reset()
}

func (t *TestSuite) TestGetCluster() {
	var err error
	// mock
	defer t.stub.Reset()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.Init, nil)
	t.stub.StubFunc(&consul.GetAllClustersData, map[string]*consul.ClusterInfo{
		"cl1": {
			HostList: []string{"h1", "h2"},
		},
	}, nil)
	t.stub.StubFunc(&backend.Init, nil)
	proxyBackend := NewProxyBackend(t.ctrl)
	proxyBackend.EXPECT().String().Return("test backend").AnyTimes()
	t.stub.StubFunc(&backend.GetBackendList, []backend.Backend{proxyBackend}, []string{}, nil)

	t.stub.StubFunc(&consul.GetTagsInfo, make(map[string]*consul.TagInfo), nil)
	watchCh := make(chan string)
	go func() {
		watchCh <- "changed"
	}()
	t.stub.StubFunc(&consul.WatchTagChange, watchCh, nil)

	// 测试
	// 初始化
	err = consul.Init("", "", nil, "")
	t.Nil(err)
	err = backend.Init(context.Background())
	t.Nil(err)
	err = cluster.Init(context.Background())
	t.Nil(err)
	// 模仿http的刷新触发
	err = cluster.Refresh()
	t.Nil(err)
	clu, err := cluster.GetCluster("cl1")
	t.Nil(err)
	t.Equal("cl1", clu.GetName())
}
