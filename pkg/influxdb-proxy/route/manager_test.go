// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route_test

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
)

func TestRouteRun(t *testing.T) {
	suite.Run(t, &TestRouteSuite{})
}

// 测试路由的初始化，以及路由是否能正确命中
type TestRouteSuite struct {
	suite.Suite
	ctrl *gomock.Controller
	stub *gostub.Stubs
}

func (t *TestRouteSuite) SetupSuite() {
	t.ctrl = gomock.NewController(t.T())
	t.stub = gostub.New()
}

func (t *TestRouteSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stub.Reset()
}

func (t *TestRouteSuite) TestRoute() {
	var err error
	// mock步骤
	defer t.stub.Reset()
	// mock掉consul接口，传入伪造数据进行route的初始化
	t.stub.StubFunc(&consul.GetAllRoutesData, map[string]*consul.RouteInfo{
		"db1.t1": {
			Cluster: "cl1",
		},
	}, nil)
	// mock掉cluster的接口，获取伪造的cluster实例
	mockCluster := NewMockCluster(t.ctrl)
	mockCluster.EXPECT().GetName().Return("cl1").AnyTimes()
	mockCluster.EXPECT().String().Return("cl1").AnyTimes()
	t.stub.StubFunc(&cluster.GetCluster, mockCluster, nil)

	// 测试
	// 初始化
	err = route.Init(context.Background())
	t.Nil(err)
	// 模仿http的更新触发机制
	err = route.Refresh()
	t.Nil(err)
	str := "db1.t1"
	clu, err := route.GetClusterByRoute(0, str)
	t.Nil(err)
	t.Equal("cl1", clu.GetName())
}
