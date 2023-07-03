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
	"net/http"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/route"
)

func TestRun(t *testing.T) {
	suite.Run(t, &TestSuite{})
}

// TestSuite 测试除路由以外的基础逻辑是否正常
type TestSuite struct {
	suite.Suite
	ctrl  *gomock.Controller
	stubs *gostub.Stubs
}

func (t *TestSuite) SetupSuite() {
	t.ctrl = gomock.NewController(t.T())
	t.stubs = gostub.New()

	// 将路由结果mock为伪造的cluster，默认路由成功
	mockCluster := mocktest.NewMockCluster(t.ctrl)
	mockCluster.EXPECT().CreateDatabase(gomock.Any(), gomock.Any(), gomock.Any()).Return(cluster.NewResponse("success", 200), nil).AnyTimes()
	mockCluster.EXPECT().Write(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(flow uint64, urlParams *cluster.WriteParams, header http.Header) (*cluster.Response, error) {
		// 这里可以打印reader来确认样例数据的正确性
		return cluster.NewResponse("success", 200), nil
	}).AnyTimes()
	mockCluster.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).Return(cluster.NewResponse("success", 200), nil).AnyTimes()
	mockCluster.EXPECT().String().Return("cl1").AnyTimes()
	mockCluster.EXPECT().GetInfluxVersion().Return("1.0.1").AnyTimes()
	mockCluster.EXPECT().GetName().Return("cl1").AnyTimes()

	t.stubs = gostub.StubFunc(&route.GetClusterByRoute, mockCluster, nil)
	t.stubs = gostub.StubFunc(&route.GetClusterByName, mockCluster, nil)
}

func (t *TestSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stubs.Reset()
}

func (t *TestSuite) TestQuery() {
	header := make(http.Header)
	params := route.NewQueryParams("db1", "select * from base", "", "", "", "", "", header, 0)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  "test",
		"flow_id": 0,
	})
	result := route.Query(params, flowLog)
	t.Equal(200, result.Code)
	t.Equal("success", result.Message)
}

func (t *TestSuite) TestWrite() {
	header := make(http.Header)
	params := route.NewWriteParams("db1", "", "", "", []byte("base,aab=bcc 3\n"), header, 0)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  "test",
		"flow_id": 0,
	})
	result := route.Write(params, flowLog)
	t.Equal(200, result.Code)
	t.Equal("success", result.Message)
}

func (t *TestSuite) TestCreateDB() {
	header := make(http.Header)
	params := route.NewCreateDBParams("db1", "cluster1", header, 0)
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  "test",
		"flow_id": 0,
	})
	result := route.CreateDB(params, flowLog)
	t.Equal(200, result.Code)
	t.Equal("success", result.Message)
}
