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
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster/routecluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

func TestRun(t *testing.T) {
	suite.Run(t, &TestSuite{})
	suite.Run(t, &TestTagManagerSuite{})
}

type TestSuite struct {
	suite.Suite
	ctrl    *gomock.Controller
	stub    *gostub.Stubs
	req     *http.Request
	cluster cluster.Cluster
}

func (t *TestSuite) SetupSuite() {
	var err error
	t.ctrl = gomock.NewController(t.T())
	t.stub = gostub.New()
	t.req, err = http.NewRequest("POST", "httpL//127.0.0.1:8080", strings.NewReader("ddd"))
	t.Nil(err)
	backend1 := NewProxyBackend(t.ctrl)
	// createDataBaseResult := "created"
	backend1.EXPECT().CreateDatabase(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backend1.EXPECT().GetVersion().Return("1.0.1").AnyTimes()
	backend1.EXPECT().Name().Return("test1").AnyTimes()
	pingDuration, err := time.ParseDuration("3s")
	t.Nil(err)
	backend1.EXPECT().Ping(gomock.Any()).Return(pingDuration, "success1", nil).AnyTimes()
	backend1.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backend1.EXPECT().Readable().Return(true).AnyTimes()
	backend1.EXPECT().String().Return("b1").AnyTimes()
	backend1.EXPECT().Write(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()

	backend2 := NewProxyBackend(t.ctrl)
	backend2.EXPECT().CreateDatabase(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backend2.EXPECT().GetVersion().Return("1.0.1").AnyTimes()
	backend2.EXPECT().Name().Return("test2").AnyTimes()
	pingDuration2, err := time.ParseDuration("3s")
	t.Nil(err)

	backend2.EXPECT().Ping(gomock.Any()).Return(pingDuration2, "success2", nil).AnyTimes()
	backend2.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backend2.EXPECT().Readable().Return(true).AnyTimes()
	backend2.EXPECT().String().Return("b2").AnyTimes()
	backend2.EXPECT().Write(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()

	backend3 := NewProxyBackend(t.ctrl)
	backend3.EXPECT().CreateDatabase(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backend3.EXPECT().GetVersion().Return("1.0.1").AnyTimes()
	backend3.EXPECT().Name().Return("test3").AnyTimes()
	pingDuration3, err := time.ParseDuration("3s")
	t.Nil(err)
	backend3.EXPECT().Ping(gomock.Any()).Return(pingDuration3, "success2", nil).AnyTimes()
	backend3.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), backend.ErrNetwork).AnyTimes()
	backend3.EXPECT().Readable().Return(true).AnyTimes()
	backend3.EXPECT().String().Return("b2").AnyTimes()
	backend3.EXPECT().Write(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()

	backend4 := NewProxyBackend(t.ctrl)
	backend4.EXPECT().CreateDatabase(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backend4.EXPECT().GetVersion().Return("1.0.1").AnyTimes()
	backend4.EXPECT().Name().Return("test4").AnyTimes()
	pingDuration4, err := time.ParseDuration("3s")
	t.Nil(err)
	backend4.EXPECT().Ping(gomock.Any()).Return(pingDuration4, "success2", nil).AnyTimes()
	backend4.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), errors.New("fake_error")).AnyTimes()
	backend4.EXPECT().Readable().Return(true).AnyTimes()
	backend4.EXPECT().String().Return("b2").AnyTimes()
	backend4.EXPECT().Write(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(backend.NewResponse("success", 200), nil).AnyTimes()
	backendList := []backend.Backend{backend1, backend2, backend3, backend4}

	// mock掉consul的获取tag接口，直接传入数据用于tag初始化
	tagInfos := map[string]*consul.TagInfo{
		"db1/table1/bk_biz_id==2": {
			HostList: []string{"test1", "test2"},
		},
		"db1/table1/bk_biz_id==3": {
			HostList: []string{"test1", "test2"},
		},
		"db1/table1/bk_biz_id==4": {
			HostList: []string{"test3", "test2"},
		},
		"db1/table1/bk_biz_id==5": {
			HostList: []string{"test4", "test2"},
		},
	}
	t.stub.StubFunc(&consul.GetTagsInfo, tagInfos, nil)
	watchCh := make(chan string)
	go func() {
		watchCh <- "changed"
	}()
	t.stub.StubFunc(&consul.WatchTagChange, watchCh, nil)
	t.cluster, err = routecluster.NewRouteCluster(context.Background(), "test", backendList, make(map[string]bool))
	t.Nil(err)
}

func (t *TestSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stub.Reset()
}

func (t *TestSuite) TestQuery() {
	header := t.req.Header
	resp, err := t.cluster.Query(0, cluster.NewQueryParams("db1", "table1", "sql1", "", "", "", "", nil), header)
	t.Nil(err)
	t.Equal("success", resp.Result)
}

func (t *TestSuite) TestWrite() {
	header := t.req.Header
	// 数据只有在最后被read的时候才会读取，所以这里给个假的也不会越界
	allData := []byte{}
	points := common.Points{
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       0,
			End:         100,
		},
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       200,
			End:         300,
		},
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       200,
			End:         300,
		},
	}
	_, err := t.cluster.Write(0, cluster.NewWriteParams("db1", "", "", "", points, allData, nil), header)
	t.Nil(err)
}

func (t *TestSuite) TestTagQuery() {
	header := t.req.Header
	resp, err := t.cluster.Query(0, cluster.NewQueryParams("db1", "table1", "select * from table1 where bk_biz_id='2'", "", "", "", "", []string{"bk_biz_id"}), header)
	t.Nil(err)
	t.Equal("success", resp.Result)
}

func (t *TestSuite) TestNetErrorTagQuery() {
	header := t.req.Header
	resp, err := t.cluster.Query(0, cluster.NewQueryParams("db1", "table1", "select * from table1 where bk_biz_id='4'", "", "", "", "", []string{"bk_biz_id"}), header)
	t.Nil(err)
	t.Equal("success", resp.Result)
}

func (t *TestSuite) TestErrorTagQuery() {
	header := t.req.Header
	_, err := t.cluster.Query(0, cluster.NewQueryParams("db1", "table1", "select * from table1 where bk_biz_id='5'", "", "", "", "", []string{"bk_biz_id"}), header)
	t.NotNil(err)
}

func (t *TestSuite) TestMissedTagQuery() {
	header := t.req.Header
	resp, err := t.cluster.Query(0, cluster.NewQueryParams("db1", "table1", "select * from table1 where bk_biz_id='9'", "", "", "", "", []string{"bk_biz_id"}), header)
	t.Equal(routecluster.ErrMatchBackendByTag, err)
	t.Nil(resp)
}

func (t *TestSuite) TestTagWrite() {
	header := t.req.Header
	tagNames := []string{"bk_biz_id"}
	// 数据只有在最后被read的时候才会读取，所以这里给个假的也不会越界
	allData := []byte{}
	points := common.Points{
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       0,
			End:         100,
			Tags: common.Tags{
				{
					Key:   []byte("bk_biz_id"),
					Value: []byte("2"),
				},
			},
		},
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       200,
			End:         300,
			Tags: common.Tags{
				{
					Key:   []byte("bk_biz_id"),
					Value: []byte("2"),
				},
			},
		},
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       200,
			End:         300,
			Tags: common.Tags{
				{
					Key:   []byte("bk_biz_id"),
					Value: []byte("3"),
				},
			},
		},
	}
	_, err := t.cluster.Write(0, cluster.NewWriteParams("db1", "", "", "", points, allData, tagNames), header)
	t.Nil(err)
}

func (t *TestSuite) TestMissedTagWrite() {
	header := t.req.Header
	tagNames := []string{"bk_biz_id"}
	// 数据只有在最后被read的时候才会读取，所以这里给个假的也不会越界
	allData := []byte{}
	points := common.Points{
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       0,
			End:         100,
			Tags: common.Tags{
				{
					Key:   []byte("bk_biz_id"),
					Value: []byte("4"),
				},
			},
		},
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       200,
			End:         300,
			Tags: common.Tags{
				{
					Key:   []byte("bk_biz_id"),
					Value: []byte("6"),
				},
			},
		},
		{
			DB:          "db1",
			Measurement: "table1",
			Start:       200,
			End:         300,
			Tags: common.Tags{
				{
					Key:   []byte("bk_biz_id"),
					Value: []byte("4"),
				},
			},
		},
	}
	_, err := t.cluster.Write(0, cluster.NewWriteParams("db1", "", "", "", points, allData, tagNames), header)
	t.Equal(routecluster.ErrMatchBackendByTag, err)
}

func (t *TestSuite) TestCreateDatabase() {
	header := t.req.Header
	resp, err := t.cluster.CreateDatabase(0, cluster.NewQueryParams("", "", "sql1", "", "", "", "", nil), header)
	t.Nil(err)
	t.Equal("success", resp.Result)
}

func (t *TestSuite) TestGetInfluxVersion() {
	res := t.cluster.GetInfluxVersion()
	t.Equal("1.0.1", res)
}
