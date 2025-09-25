// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package base_test

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/hashicorp/consul/api"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul/base"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

type TestSuite struct {
	ctrl *gomock.Controller
	stub *gostub.Stubs
	suite.Suite
}

func TestRun(t *testing.T) {
	suite.Run(t, &TestSuite{})
}

func (t *TestSuite) SetupSuite() {
	t.ctrl = gomock.NewController(t.T())

	mockKV := NewMockAbstractKV(t.ctrl)
	mockKV.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	getKVPair := &api.KVPair{
		Key:   "testK",
		Value: []byte("testV"),
	}
	mockKV.EXPECT().Get(gomock.Any(), gomock.Any()).Return(getKVPair, nil, nil).AnyTimes()
	getKVPairList := api.KVPairs{getKVPair}
	mockKV.EXPECT().List(gomock.Any(), gomock.Any()).Return(getKVPairList, nil, nil).AnyTimes()
	keys := []string{"a", "b", "c"}
	mockKV.EXPECT().Keys(gomock.Any(), gomock.Any(), gomock.Any()).Return(keys, nil, nil).AnyTimes()

	mockAgent := NewMockAbstractAgent(t.ctrl)
	mockAgent.EXPECT().CheckRegister(gomock.Any()).Return(nil).AnyTimes()
	mockAgent.EXPECT().CheckDeregister(gomock.Any()).Return(nil).AnyTimes()
	mockAgent.EXPECT().ServiceRegister(gomock.Any()).Return(nil).AnyTimes()
	mockAgent.EXPECT().ServiceDeregister(gomock.Any()).Return(nil).AnyTimes()

	mockAgent.EXPECT().AgentHealthServiceByID(gomock.Any()).Return("passing", new(api.AgentServiceChecksInfo), nil).AnyTimes()
	mockAgent.EXPECT().ChecksWithFilter(gomock.Any()).Return(map[string]*api.AgentCheck{"t1": {Status: "passing"}}, nil).AnyTimes()
	mockAgent.EXPECT().PassTTL(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockAgent.EXPECT().FailTTL(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	mockPlan := NewMockAbstractPlan(t.ctrl)
	mockPlan.EXPECT().Run(gomock.Any()).Return(nil).AnyTimes()
	mockPlan.EXPECT().IsStopped().Return(false).AnyTimes()
	mockPlan.EXPECT().Stop().Return().AnyTimes()

	t.stub = gostub.Stub(&base.GetAPI, func(client *base.BasicClient, address string, scheme string, skip_verify bool) error {
		client.KV = mockKV
		client.Agent = mockAgent
		return nil
	})
	t.stub.StubFunc(&base.GetPlan, mockPlan, nil)
}

func (t *TestSuite) TearDownSuite() {
	t.stub.Reset()
	t.ctrl.Finish()
}

func (t *TestSuite) TestPut() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	err = client.Put("REDIS_MAXCLIENTS/TestDir1/test5", []byte("testahaha234"))
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestGet() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	v, err := client.Get("testK")
	if err != nil {
		t.Error(err)
	}
	t.Equal("testV", string(v.Value))
}

func (t *TestSuite) TestGetPrefix() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	kvPairs, err := client.GetPrefix("REDIS_MAXCLIENTS/TestDir1", "/")
	if err != nil {
		t.Error(err)
	}
	for _, v := range kvPairs {
		t.Equal("testV", string(v.Value))
	}
}

func (t *TestSuite) TestGetChild() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	childs, err := client.GetChild("REDIS_MAXCLIENTS/TestDir1", "/")
	if err != nil {
		t.Error(err)
	}
	t.Equal([]string{"a", "b", "c"}, childs)
}

func (t *TestSuite) TestWatch() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	_, err = client.Watch("REDIS_MAXCLIENTS/TestDir1/test5", "")
	if err != nil {
		t.Error(err)
	}
	err = client.StopWatch("REDIS_MAXCLIENTS/TestDir1/test5", "path")
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestClose() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	_, err = client.Watch("REDIS_MAXCLIENTS/TestDir1/test5", "")
	if err != nil {
		t.Error(err)
	}
	_, err = client.Watch("REDIS_MAXCLIENTS/TestDir1", "/")
	if err != nil {
		t.Error(err)
	}
	err = client.Close()
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestWatchPrefix() {
	client, err := base.NewBasicClient("", nil)
	if err != nil {
		t.Error(err)
	}
	_, err = client.Watch("REDIS_MAXCLIENTS/TestDir1", "/")
	if err != nil {
		t.Error(err)
	}
	err = client.StopWatch("REDIS_MAXCLIENTS/TestDir1/test5", "prefix")
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestCheckStatus() {
	var err error
	var res string
	client, err := base.NewBasicClient("", nil)
	t.Nil(err)
	res, err = client.CheckStatus("t1")
	t.Nil(err)
	t.Equal("passing", res)
}

func (t *TestSuite) TestChangeStatus() {
	client, err := base.NewBasicClient("", nil)
	t.Nil(err)
	err = client.CheckPass("t1", "test")
	t.Nil(err)
}

func (t *TestSuite) TestCheckRegister() {
	client, err := base.NewBasicClient("", nil)
	t.Nil(err)
	err = client.ServiceAwake("influxdb_proxy")
	t.Nil(err)
	err = client.CheckRegister("influxdb_proxy", "t1", "15s")
	t.Nil(err)
}

func (t *TestSuite) TestCheckDeregister() {
	client, err := base.NewBasicClient("", nil)
	t.Nil(err)
	err = client.CheckDeregister("t1")
	t.Nil(err)
}

func (t *TestSuite) TestServiceStatus() {
	client, err := base.NewBasicClient("127.0.0.1:8500", nil)
	t.Nil(err)
	err = client.ServiceAwake("influxdb_proxy")
	t.Nil(err)
}
