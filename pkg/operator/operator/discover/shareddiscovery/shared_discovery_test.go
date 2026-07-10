// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shareddiscovery

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterDoesNotRetainReferenceWhenCreateFails(t *testing.T) {
	const uk = "failed-discovery"

	sharedDiscoveryLock.Lock()
	oldRefs := sharedDiscoveryRefs
	oldMap := sharedDiscoveryMap
	sharedDiscoveryRefs = make(map[string]int)
	sharedDiscoveryMap = make(map[string]*SharedDiscovery)
	sharedDiscoveryLock.Unlock()
	t.Cleanup(func() {
		sharedDiscoveryLock.Lock()
		sharedDiscoveryRefs = oldRefs
		sharedDiscoveryMap = oldMap
		sharedDiscoveryLock.Unlock()
	})

	err := Register(uk, func() (*SharedDiscovery, error) {
		return nil, errors.New("create failed")
	})

	assert.EqualError(t, err, "create failed")
	sharedDiscoveryLock.Lock()
	defer sharedDiscoveryLock.Unlock()
	assert.NotContains(t, sharedDiscoveryRefs, uk)
	assert.NotContains(t, sharedDiscoveryMap, uk)
}
