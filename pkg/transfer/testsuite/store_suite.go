// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testsuite

import (
	"fmt"

	"github.com/golang/mock/gomock"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
)

// StoreSuite :
type StoreSuite struct {
	ConfigSuite
	Store *MockStore
}

// StoreHost :
func (s *StoreSuite) StoreHost(host *models.CCHostInfo) *gomock.Call {
	bytes, err := models.ModelConverter.Marshal(host)
	return s.Store.EXPECT().Get(host.GetStoreKey()).Return(bytes, err)
}

// StoreAgentHost :
func (s *StoreSuite) StoreAgentHost(agentHost *models.CCAgentHostInfo) *gomock.Call {
	bytes := []byte(fmt.Sprintf("%d:%d:%s", agentHost.BizID, agentHost.CloudID, agentHost.IP))
	return s.Store.EXPECT().Get(agentHost.GetStoreKey()).Return(bytes, nil)
}

// StoreHost :
func (s *StoreSuite) StoreInstance(instanceInfo *models.CCInstanceInfo) *gomock.Call {
	bytes, err := models.ModelConverter.Marshal(instanceInfo)
	return s.Store.EXPECT().Get(instanceInfo.GetStoreKey()).Return(bytes, err)
}

// SetupTest :
func (s *StoreSuite) SetupTest() {
	s.ConfigSuite.SetupTest()
	s.Store = NewMockStore(s.Ctrl)
	s.CTX = define.StoreIntoContext(s.CTX, s.Store)
}
