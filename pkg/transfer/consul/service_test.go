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
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// ServiceSuite
type ServiceSuite struct {
	ConsulSuite
	config *ServiceConfig
}

// SetupTest
func (s *ServiceSuite) SetupTest() {
	s.ConsulSuite.SetupTest()
	s.config = &ServiceConfig{
		ID:      "id",
		Name:    "Name",
		Tags:    []string{"tag"},
		Address: "address",
		Port:    1,
		Meta: map[string]string{
			"key": "value",
		},
		TTL:             time.Millisecond,
		SessionBehavior: SessionBehaviorDelete,
		Namespace:       "namespace",
	}
}

// WithSessionOpen
func (s *ServiceSuite) WithSessionOpen() {
	s.WithSessionCreate()
	s.WithSessionRenew()
	s.WithSessionDestroy()
}

// WithSessionCreate
func (s *ServiceSuite) WithSessionCreate() *gomock.Call {
	return s.session.EXPECT().Create(gomock.Any(), gomock.Any()).Return("", &WriteMeta{}, nil)
}

// WithSessionRenew
func (s *ServiceSuite) WithSessionRenew() *gomock.Call {
	return s.session.EXPECT().Renew(gomock.Any(), gomock.Any()).Return(&SessionEntry{}, &WriteMeta{}, nil).AnyTimes()
}

// WithSessionDestroy
func (s *ServiceSuite) WithSessionDestroy() *gomock.Call {
	return s.session.EXPECT().Destroy(gomock.Any(), gomock.Any()).Return(&WriteMeta{}, nil)
}

// WithElection
func (s *ServiceSuite) WithElection(leader bool) *gomock.Call {
	return s.kv.EXPECT().Acquire(gomock.Any(), gomock.Any()).Return(leader, &WriteMeta{}, nil).AnyTimes()
}

// WithEnableMaintenance
func (s *ServiceSuite) WithEnableMaintenance() *gomock.Call {
	return s.agent.EXPECT().EnableServiceMaintenance(gomock.Any(), gomock.Any())
}

// WithDisableMaintenance
func (s *ServiceSuite) WithDisableMaintenance() *gomock.Call {
	return s.agent.EXPECT().DisableServiceMaintenance(gomock.Any())
}

// WithServiceRun
func (s *ServiceSuite) WithServiceRun(service define.Service, fn func()) {
	s.NoError(service.Start())
	fn()
	s.NoError(service.Stop())
	s.NoError(service.Wait())
}

// WithServiceRunMakeSureHeartBeat
func (s *ServiceSuite) WithServiceRunMakeSureHeartBeat(service define.Service, fn func()) {
	ch := make(chan bool)
	var once sync.Once
	s.WithHeartBeat(func() {
		once.Do(func() {
			close(ch)
		})
	})

	s.WithServiceRun(service, func() {
		fn()
		<-ch
	})
}

// WithServiceRunMakeSureAfterHeartBeat
func (s *ServiceSuite) WithServiceRunMakeSureAfterHeartBeat(service define.Service, fn func()) {
	ch := make(chan bool)
	var once sync.Once
	s.WithHeartBeat(func() {
		once.Do(func() {
			close(ch)
		})
	})

	s.WithServiceRun(service, func() {
		<-ch
		fn()
	})
}

// WithHeartBeat
func (s *ServiceSuite) WithHeartBeat(callback func()) {
	s.WithSessionCreate()
	s.WithSessionDestroy()
	s.WithSessionRenew().Do(func(id string, q *WriteOptions) (*SessionEntry, *WriteMeta, error) {
		if callback != nil {
			callback()
		}
		return nil, nil, nil
	})
}

// TestUsage
func (s *ServiceSuite) TestUsage() {
	var startWaitGroup, stopWaitGroup sync.WaitGroup

	var sessionCreateOnce sync.Once
	startWaitGroup.Add(1)
	s.WithSessionCreate().Do(func(se *SessionEntry, q *WriteOptions) (string, *WriteMeta, error) {
		sessionCreateOnce.Do(startWaitGroup.Done)
		return "", &WriteMeta{}, nil
	})

	var sessionRenewOnce sync.Once
	startWaitGroup.Add(1)
	s.WithSessionRenew().Do(func(id string, q *WriteOptions) (*SessionEntry, *WriteMeta, error) {
		sessionRenewOnce.Do(startWaitGroup.Done)
		return &SessionEntry{}, &WriteMeta{}, nil
	})

	var sessionDestroyOnce sync.Once
	stopWaitGroup.Add(1)
	s.WithSessionDestroy().Do(func(id string, q *WriteOptions) (*WriteMeta, error) {
		sessionDestroyOnce.Do(stopWaitGroup.Done)
		return &WriteMeta{}, nil
	})

	service := NewService(s.CTX, s.client, s.config)

	s.WithServiceRun(service, startWaitGroup.Wait)

	stopWaitGroup.Wait()
}

// TestDisable
func (s *ServiceSuite) TestDisable() {
	s.WithHeartBeat(nil)

	statusCh := make(chan bool)
	service := NewService(s.CTX, s.client, s.config)
	s.NoError(service.Bus.SubscribeAsync(EvDisable, func() {
		statusCh <- false
	}, false))

	s.WithServiceRun(service, func() {
		s.NoError(service.Disable())
		s.False(<-statusCh)
	})
}

// TestEnable
func (s *ServiceSuite) TestEnable() {
	s.WithHeartBeat(nil)

	statusCh := make(chan bool)
	service := NewService(s.CTX, s.client, s.config)
	s.NoError(service.Bus.SubscribeAsync(EvEnable, func() {
		statusCh <- true
	}, false))

	s.WithServiceRun(service, func() {
		s.NoError(service.Enable())
		s.True(<-statusCh)
	})
}

// TestServiceSuite
func TestServiceSuite(t *testing.T) {
	suite.Run(t, new(ServiceSuite))
}
