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
	"fmt"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ConsulSuite
type ConsulSuite struct {
	testsuite.ContextSuite
	apiError error
	client   *MockClientAPI
	kv       *MockKvAPI
	session  *MockSessionAPI
	agent    *MockAgentAPI
	health   *MockHealthAPI
	plan     *MockWatchPlan
}

// SetupTest
func (s *ConsulSuite) SetupTest() {
	s.ContextSuite.SetupTest()

	s.apiError = errors.New("api error")

	s.plan = NewMockWatchPlan(s.Ctrl)
	s.kv = NewMockKvAPI(s.Ctrl)
	s.session = NewMockSessionAPI(s.Ctrl)
	s.agent = NewMockAgentAPI(s.Ctrl)
	s.health = NewMockHealthAPI(s.Ctrl)
	s.client = NewMockClientAPI(s.Ctrl)

	s.client.EXPECT().Raw().Return(nil).AnyTimes()
	s.client.EXPECT().KV().Return(s.kv).AnyTimes()
	s.client.EXPECT().Session().Return(s.session).AnyTimes()
	s.client.EXPECT().Agent().Return(s.agent).AnyTimes()
	s.client.EXPECT().Health().Return(s.health).AnyTimes()
}

// SessionKey
func (s *ConsulSuite) SessionKey(namespace, id, key string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, id, key)
}

// MetaKey
func (s *ConsulSuite) MetaKey(namespace, key string) string {
	return fmt.Sprintf("%s/%s", namespace, key)
}
