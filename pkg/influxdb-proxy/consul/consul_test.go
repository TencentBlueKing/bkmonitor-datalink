// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/hashicorp/consul/api"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

var totalPrefix = "influxdb_proxy"

type TestSuite struct {
	suite.Suite
	ctrl *gomock.Controller
	stub *gostub.Stubs
}

func TestRun(t *testing.T) {
	suite.Run(t, &TestSuite{})
}

func (t *TestSuite) SetupSuite() {
	t.ctrl = gomock.NewController(t.T())
	t.stub = gostub.New()
	// 这一行用于实际环境，mock环境下可以忽略
}

func (t *TestSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stub.Reset()
}

func (t *TestSuite) TestRelease() {
	// mock
	mockClient := NewMockConsulClient(t.ctrl)
	mockClient.EXPECT().Close().Return(nil).AnyTimes()
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	t.Nil(consul.Release())
}

func (t *TestSuite) TestGetDBsName() {
	// mock步骤
	RouteBase := consul.RouteBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	d1 := totalPrefix + "/" + RouteBase + "/db1/"
	d2 := totalPrefix + "/" + RouteBase + "/db2/"
	d3 := totalPrefix + "/" + RouteBase + "/db3/"
	childList := []string{d1, d2, d3}
	mockClient.EXPECT().GetChild(gomock.Any(), gomock.Any()).Return(childList, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	names, err := consul.GetDBsName()
	if err != nil {
		t.Error(err)
	}
	// 验证
	t.Equal([]string{"db1", "db2", "db3"}, names)
	// fmt.Println(names)
}

func (t *TestSuite) TestGetTablesName() {
	// mock步骤
	RouteBase := consul.RouteBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	t1 := totalPrefix + "/" + RouteBase + "/db1/t1"
	t2 := totalPrefix + "/" + RouteBase + "/db1/t2"
	childList := []string{t1, t2}
	mockClient.EXPECT().GetChild(gomock.Any(), gomock.Any()).Return(childList, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	names, err := consul.GetTablesName("db1")
	if err != nil {
		t.Error(err)
	}
	// 验证
	t.Equal([]string{"t1", "t2"}, names)
	// fmt.Println(names)
}

func (t *TestSuite) TestGetClustersName() {
	// mock步骤
	clusterBase := consul.ClusterBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	c1 := totalPrefix + "/" + clusterBase + "/cl1"
	c2 := totalPrefix + "/" + clusterBase + "/cl2"
	c3 := totalPrefix + "/" + clusterBase + "/cl3"
	childList := []string{c1, c2, c3}
	mockClient.EXPECT().GetChild(gomock.Any(), gomock.Any()).Return(childList, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	names, err := consul.GetClustersName()
	if err != nil {
		t.Error(err)
	}

	// 验证
	t.Equal([]string{"cl1", "cl2", "cl3"}, names)
}

func (t *TestSuite) TestGetHostsName() {
	// mock步骤
	hostBase := consul.HostBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	h1 := totalPrefix + "/" + hostBase + "/h1"
	h2 := totalPrefix + "/" + hostBase + "/h2"
	h3 := totalPrefix + "/" + hostBase + "/h3"
	childList := []string{h1, h2, h3}
	mockClient.EXPECT().GetChild(gomock.Any(), gomock.Any()).Return(childList, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	names, err := consul.GetHostsName()
	if err != nil {
		t.Error(err)
	}
	// 验证
	t.Equal([]string{"h1", "h2", "h3"}, names)
	// fmt.Println(names)
}

func (t *TestSuite) TestGetRouteInfo() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	message := &api.KVPair{
		Key:   "",
		Value: []byte(`{"cluster":"cl1"}`),
	}

	mockClient.EXPECT().Get(gomock.Any()).Return(message, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	info, err := consul.GetRouteInfo("db1", "t1")
	if err != nil {
		t.Error(err)
	}

	res := info.Cluster
	t.Equal("cl1", res)
}

func (t *TestSuite) TestGetClusterInfo() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	message := &api.KVPair{
		Key: "",
		Value: []byte(`{
			"host_list": [
				"h1",
				"h2"
			]
		  }`),
	}
	mockClient.EXPECT().Get(gomock.Any()).Return(message, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	info, err := consul.GetClusterInfo("cl1")
	if err != nil {
		t.Error(err)
	}

	// 验证
	res := info.HostList
	t.Equal([]string{"h1", "h2"}, res)
	// fmt.Println(info)
}

func (t *TestSuite) TestGetHostInfo() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	message := &api.KVPair{
		Key: "",
		Value: []byte(`{
			"username": "username3",
			"password":"password",
			"domain_name":"127.0.0.1",
			"port":8000
		  }`),
	}
	mockClient.EXPECT().Get(gomock.Any()).Return(message, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	info, err := consul.GetHostInfo("h3")
	if err != nil {
		t.Error(err)
	}

	// 验证

	username := info.Username
	passowrd := info.Password
	domainname := info.DomainName
	port := info.Port
	t.Equal("username3", username)
	t.Equal("password", passowrd)
	t.Equal("127.0.0.1", domainname)
	t.Equal(8000, port)
	// fmt.Println(info)
}

func (t *TestSuite) TestGetAllRoutesData() {
	// mock
	RouteBase := consul.RouteBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	t1 := totalPrefix + "/" + RouteBase + "/db1/t1"
	t2 := totalPrefix + "/" + RouteBase + "/db1/t2"
	t3 := totalPrefix + "/" + RouteBase + "/db2/t3"
	message := api.KVPairs{
		&api.KVPair{
			Key:   t1,
			Value: []byte(`{"cluster":"cl1"}`),
		},
		&api.KVPair{
			Key:   t2,
			Value: []byte(`{"cluster":"cl2"}`),
		},
		&api.KVPair{
			Key:   t3,
			Value: []byte(`{"cluster":"cl3"}`),
		},
	}

	mockClient.EXPECT().GetPrefix(gomock.Any(), gomock.Any()).Return(message, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	result, err := consul.GetAllRoutesData()
	if err != nil {
		t.Error(err)
	}

	// 验证
	cluster1 := result["db1.t1"].Cluster
	cluster2 := result["db1.t2"].Cluster
	cluster3 := result["db2.t3"].Cluster
	t.Equal("cl1", cluster1)
	t.Equal("cl2", cluster2)
	t.Equal("cl3", cluster3)
	// fmt.Println(result)
}

func (t *TestSuite) TestGetAllClustersData() {
	// mock步骤
	clusterBase := consul.ClusterBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	c1 := totalPrefix + "/" + clusterBase + "/cl1"
	c2 := totalPrefix + "/" + clusterBase + "/cl2"
	c3 := totalPrefix + "/" + clusterBase + "/cl3"
	message := api.KVPairs{
		&api.KVPair{
			Key: c1,
			Value: []byte(`{
			"host_list": [
				"h1",
				"h2"
			]
		  }`),
		},
		&api.KVPair{
			Key: c2,
			Value: []byte(`{
			"host_list": [
				"h2",
				"h3"
			]
		  }`),
		},
		&api.KVPair{
			Key: c3,
			Value: []byte(`{
			"host_list": [
				"h3",
				"h4"
			]
		  }`),
		},
	}
	mockClient.EXPECT().GetPrefix(gomock.Any(), gomock.Any()).Return(message, nil).AnyTimes()
	defer t.stub.Reset()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)

	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	result, err := consul.GetAllClustersData()
	if err != nil {
		t.Error(err)
	}

	// 验证
	r1 := []string{
		"h1",
		"h2",
	}
	r2 := []string{
		"h2",
		"h3",
	}
	r3 := []string{
		"h3",
		"h4",
	}

	hostlist1 := result["cl1"].HostList
	hostlist2 := result["cl2"].HostList
	hostlist3 := result["cl3"].HostList
	t.Equal(r1, hostlist1)
	t.Equal(r2, hostlist2)
	t.Equal(r3, hostlist3)
	// fmt.Println(result)
}

func (t *TestSuite) TestGetAllHostsData() {
	// mock
	hostBase := consul.HostBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	h1 := totalPrefix + "/" + hostBase + "/h1"
	h2 := totalPrefix + "/" + hostBase + "/h2"
	h3 := totalPrefix + "/" + hostBase + "/h3"
	message := api.KVPairs{
		&api.KVPair{
			Key: h1,
			Value: []byte(`{
			"username": "username1",
			"password":"passowrd",
			"domain_name":"127.0.0.1",
			"port":8000
			}`),
		},
		&api.KVPair{
			Key: h2,
			Value: []byte(`{
				"username": "username2",
				"password":"passowrd",
				"domain_name":"127.0.0.1",
				"port":8001
				}`),
		},
		&api.KVPair{
			Key: h3,
			Value: []byte(`{
				"username": "username3",
				"password":"passowrd",
				"domain_name":"127.0.0.1",
				"port":8002
				}`),
		},
	}

	mockClient.EXPECT().GetPrefix(gomock.Any(), gomock.Any()).Return(message, nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	result, err := consul.GetAllHostsData()
	if err != nil {
		t.Error(err)
	}

	// 验证
	port1 := result["h1"].Port
	port2 := result["h2"].Port
	port3 := result["h3"].Port
	t.Equal(8000, port1)
	t.Equal(8001, port2)
	t.Equal(8002, port3)
	// fmt.Println(result)
}

func (t *TestSuite) TestInitCluster() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	mockClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	defer t.stub.Reset()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)

	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	clusterPath := consul.ClusterBasePath

	err = consul.Put(totalPrefix+"/"+clusterPath+"/"+"cl1", []byte(`{
		"host_list": [
			"h1",
			"h2
		]
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+clusterPath+"/"+"cl2", []byte(`{
		"host_list": [
			"h3"
		]
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+clusterPath+"/"+"cl3", []byte(`{
		"host_list": [
			"h3",
			"h4"
		]
	  }`))
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestInitHost() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	mockClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	hostPath := consul.HostBasePath

	err = consul.Put(totalPrefix+"/"+hostPath+"/"+"h1", []byte(`{
		"domain_name":"127.0.0.1",
		"port":8086
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+hostPath+"/"+"h2", []byte(`{
		"domain_name":"127.0.0.1",
		"port":8087
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+hostPath+"/"+"h3", []byte(`{
		"username":"username3",
		"password":"password",
		"domain_name":"127.0.0.1",
		"port":8002
	  }`))
	err = consul.Put(totalPrefix+"/"+hostPath+"/"+"h4", []byte(`{
		"username":"username4",
		"password":"password",
		"domain_name":"127.0.0.1",
		"port":8002
	  }`))
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestInitRoutes() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	mockClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// 注释此行可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()
	// 测试
	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	RoutePath := consul.RouteBasePath

	err = consul.Put(totalPrefix+"/"+RoutePath+"/db1"+"/t1", []byte(`{
		"cluster": "cl1"
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+RoutePath+"/db1"+"/t2", []byte(`{
		"cluster": "cl2"
	  }`))
	if err != nil {
		t.Error(err)
	}

	err = consul.Put(totalPrefix+"/"+RoutePath+"/db2"+"/t3", []byte(`{
		"cluster": "cl3"
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+RoutePath+"/db2"+"/t4", []byte(`{
		"cluster": "cl4"
	  }`))
	if err != nil {
		t.Error(err)
	}
	err = consul.Put(totalPrefix+"/"+RoutePath+"/db3"+"/t5", []byte(`{
		"cluster": "cl5"
	  }`))
	if err != nil {
		t.Error(err)
	}
}

func (t *TestSuite) TestDeleteHost() {
	// mock步骤
	mockClient := NewMockConsulClient(t.ctrl)
	mockClient.EXPECT().Delete(gomock.Any()).Return(nil).AnyTimes()
	// 注释此行可以测试实际环境

	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	defer t.stub.Reset()

	hostPath := consul.HostBasePath

	err := consul.Init("127.0.0.1:8500", totalPrefix, "", "", "", false)
	if err != nil {
		t.Error(err)
	}
	err = consul.Delete(totalPrefix + "/" + hostPath + "/" + "h2")
	if err != nil {
		t.Error(err)
	}
}
