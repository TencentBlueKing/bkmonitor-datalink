// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend_test

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/hashicorp/consul/api"
	"github.com/prashantv/gostub"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb"
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
}

func (t *TestSuite) TearDownSuite() {
	t.ctrl.Finish()
	t.stub.Reset()
}

func (t *TestSuite) TestManageBackend() {
	// mock
	hostBase := consul.HostBasePath
	mockClient := NewMockConsulClient(t.ctrl)
	message := api.KVPairs{
		&api.KVPair{
			Key: consul.TotalPrefix + "/" + hostBase + "/h1",
			Value: []byte(`{
				"username": "username1",
				"password":"password",
				"domain_name":"127.0.0.1",
				"port":8000
				}`),
		},
		&api.KVPair{
			Key: consul.TotalPrefix + "/" + hostBase + "/h2",
			Value: []byte(`{
					"username": "username2",
					"password":"password",
					"domain_name":"127.0.0.1",
					"port":8001
					}`),
		},
		&api.KVPair{
			Key: consul.TotalPrefix + "/" + hostBase + "/h3",
			Value: []byte(`{
					"username": "username3",
					"password":"password",
					"domain_name":"127.0.0.1",
					"port":8002
					}`),
		},
	}

	mockClient.EXPECT().GetPrefix(gomock.Any(), gomock.Any()).Return(message, nil).AnyTimes()
	mockClient.EXPECT().Watch(gomock.Any(), gomock.Any()).Return(make(<-chan interface{}), nil).AnyTimes()
	mockClient.EXPECT().Close().Return(nil).AnyTimes()
	proxyBackend := NewProxyBackend(t.ctrl)
	proxyBackend.EXPECT().String().Return("proxy_backend").AnyTimes()
	defer t.stub.Reset()

	// 注释下面部分可以测试实际环境
	t.stub.StubFunc(&consul.GetConsulClient, mockClient, nil)
	t.stub.StubFunc(&influxdb.NewBackend, proxyBackend, nil, nil)

	// 测试
	viper.Set("kafka.address", "127.0.0.1")
	viper.Set("kafka.port", "9092")
	viper.Set("kafka.topic_prefix", "bkmonitor")
	viper.Set("kafka.version", "0.10.2.0")
	var err error
	err = consul.Init("127.0.0.1:8500", consul.TotalPrefix, nil)
	t.Nil(err)
	err = backend.Init(context.Background())
	t.Nil(err)
	list := []string{"h1", "h2", "h3", "h4"}
	backends, empty, err := backend.GetBackendList(list)
	t.Equal(backend.ErrBackendNotExistInList, err)
	t.NotNil(backends)
	t.NotNil(empty)
}
