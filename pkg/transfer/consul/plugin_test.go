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
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	consul "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/suite"

	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// LeaderMixinSuite
type LeaderMixinSuite struct {
	ContextSuite
	config  ServiceConfig
	client  *MockClientAPI
	service *Service
}

// WithPromote
func (s *LeaderMixinSuite) WithPromote(fn func()) {
	bus, err := GetServiceEventBus(s.service)
	s.NoError(err)
	go func() {
		bus.Publish(EvPromoted, s.config.ID)
		if fn != nil {
			fn()
		}
	}()
}

// WithRetire
func (s *LeaderMixinSuite) WithRetire(fn func()) {
	bus, err := GetServiceEventBus(s.service)
	s.NoError(err)
	go func() {
		bus.Publish(EvRetired, s.config.ID)
		if fn != nil {
			fn()
		}
	}()
}

// SetupTest
func (s *LeaderMixinSuite) SetupTest() {
	s.ContextSuite.SetupTest()
	s.client = NewMockClientAPI(s.Ctrl)
	s.service = NewService(s.CTX, s.client, &s.config)
}

// PluginSuite
type PluginSuite struct {
	ServiceSuite
	root *Service
}

// SetupTest
func (s *PluginSuite) SetupTest() {
	s.ServiceSuite.SetupTest()
	s.root = NewService(s.CTX, s.client, s.config)
}

// WithWrap
func (s *PluginSuite) WithWrap(plugin ServicePlugin) {
	s.NoError(plugin.Wrap(s.root))
}

// TestWrap
func (s *PluginSuite) TestWrap() {
	plugin := NewMockServicePlugin(s.Ctrl)
	plugin.EXPECT().Wrap(gomock.Any()).AnyTimes()
	plugin.EXPECT().Root().Return(s.root).AnyTimes()

	cases := []struct {
		service define.Service
		err     error
	}{
		{s.root, nil},
		{plugin, nil},
		{nil, define.ErrType},
		{NewMockService(s.Ctrl), define.ErrType},
	}

	for i, c := range cases {
		plugin := NewBaseServicePlugin()
		s.Equal(c.err, errors.Cause(plugin.Wrap(c.service)), i)
		if c.err == nil {
			s.Equal(s.root, plugin.Root(), i)
		}
	}
}

// TestPluginSuite
func TestPluginSuite(t *testing.T) {
	suite.Run(t, new(PluginSuite))
}

// ElectionPluginSuite
type ElectionPluginSuite struct {
	PluginSuite
}

// TestWrap
func (s *ElectionPluginSuite) TestWrap() {
	s.WithHeartBeat(nil)

	plugin := NewElectionPlugin()
	s.WithWrap(plugin)

	ch := make(chan bool)
	var once sync.Once
	s.WithElection(true).Do(func(p *KVPair, q *WriteOptions) (bool, *WriteMeta, error) {
		once.Do(func() {
			close(ch)
		})
		return true, nil, nil
	})

	s.WithServiceRun(plugin, func() {
		<-ch
	})
}

// TestElectionPluginSuite
func TestElectionPluginSuite(t *testing.T) {
	suite.Run(t, new(ElectionPluginSuite))
}

// MaintenancePluginSuite
type MaintenancePluginSuite struct {
	PluginSuite
}

// TestEnable
func (s *MaintenancePluginSuite) TestEnable() {
	plugin := NewMaintenancePlugin()
	s.WithWrap(plugin)

	s.WithServiceRunMakeSureHeartBeat(plugin, func() {
		s.WithDisableMaintenance()
		s.NoError(plugin.Enable())
	})
}

// TestDisable
func (s *MaintenancePluginSuite) TestDisable() {
	plugin := NewMaintenancePlugin()
	s.WithWrap(plugin)

	s.WithServiceRunMakeSureHeartBeat(plugin, func() {
		s.WithEnableMaintenance()
		s.NoError(plugin.Disable())
	})
}

// TestMaintenancePluginSuite
func TestMaintenancePluginSuite(t *testing.T) {
	suite.Run(t, new(MaintenancePluginSuite))
}

// TTLCheckPluginSuite
type TTLCheckPluginSuite struct {
	PluginSuite
}

// WithUpdateTTL
func (s *TTLCheckPluginSuite) WithUpdateTTL() *gomock.Call {
	return s.agent.EXPECT().UpdateTTL(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
}

// WithRunUpdateTTL
func (s *TTLCheckPluginSuite) WithRunUpdateTTL(plugin ServicePlugin, excepted string, fn func()) {
	s.WithUpdateTTL().Do(func(checkID, output, status string) error {
		s.Equal(excepted, status)
		return nil
	})

	s.WithServiceRunMakeSureHeartBeat(plugin, fn)
}

// TestHeartBeat
func (s *TTLCheckPluginSuite) TestHeartBeat() {
	plugin := NewTTLCheckPlugin()
	s.WithWrap(plugin)
	s.WithRunUpdateTTL(plugin, consul.HealthPassing, func() {
	})
}

// TestEnable
func (s *TTLCheckPluginSuite) TestEnable() {
	plugin := NewTTLCheckPlugin()
	s.WithWrap(plugin)
	s.NoError(plugin.Enable())
	s.WithRunUpdateTTL(plugin, consul.HealthPassing, func() {
	})
}

// TestDisable
func (s *TTLCheckPluginSuite) TestDisable() {
	plugin := NewTTLCheckPlugin()
	s.WithWrap(plugin)
	s.NoError(plugin.Disable())
	s.WithRunUpdateTTL(plugin, consul.HealthCritical, func() {
	})
}

// TestTTLCheckPluginSuite
func TestTTLCheckPluginSuite(t *testing.T) {
	suite.Run(t, new(TTLCheckPluginSuite))
}
