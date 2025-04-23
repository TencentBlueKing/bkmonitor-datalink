// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/esb"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

func newESBConfig() define.Configuration {
	conf := config.NewConfiguration()
	conf.SetDefault(esb.ConfESBAppCodeKey, "bkmonitor")
	conf.SetDefault(esb.ConfESBAppSecretKey, "test")
	conf.SetDefault(esb.ConfESBUserNameKey, "admin")
	conf.SetDefault(esb.ConfESBAddress, "http://paas.service.consul")
	return conf
}

func newJSONResponse(status int, json string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(json)),
	}
}

// ClientSuite :
type ClientSuite struct {
	suite.Suite
	ctrl   *gomock.Controller
	client *esb.Client
	conf   define.Configuration
	doer   *MockDoer
}

// SetupTest :
func (s *ClientSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
	s.conf = newESBConfig()
	s.doer = NewMockDoer(s.ctrl)
	s.client = esb.NewClientWithDoer(s.conf, s.doer)
}

// TearDownTest :
func (s *ClientSuite) TearDownTest() {
	s.ctrl.Finish()
}

// TestAgent :
func (s *ClientSuite) TestAgent() {
	req, err := s.client.Agent().Request()

	s.NoError(err)
	s.Equal("http://paas.service.consul", req.URL.String())
}

// TestCommonArgs :
func (s *ClientSuite) TestCommonArgs() {
	args := s.client.CommonArgs()
	s.Equal(s.conf.Get(esb.ConfESBAppCodeKey), args.AppCode)
	s.Equal(s.conf.Get(esb.ConfESBAppSecretKey), args.AppSecret)
	s.Equal(s.conf.Get(esb.ConfESBUserNameKey), args.UserName)
}

// TestClientSuite :
func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
