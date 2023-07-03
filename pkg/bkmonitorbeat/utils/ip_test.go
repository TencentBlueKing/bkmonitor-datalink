// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
)

func TestCheckIpOrDomainValid(t *testing.T) {
	s := "1.1.1.1"
	ret := utils.CheckIpOrDomainValid(s)
	assert.Equal(t, ret, utils.V4)

	s = "1.1.1.1:11"
	ret = utils.CheckIpOrDomainValid(s)
	assert.Equal(t, ret, utils.V4)

	s = "f::f:f:f:f:f"
	ret = utils.CheckIpOrDomainValid(s)
	assert.Equal(t, ret, utils.V6)

	s = "[f::f:f:f:f:f]:9000"
	ret = utils.CheckIpOrDomainValid(s)
	assert.Equal(t, ret, utils.V6)

	s = "www.baidu.com:9000"
	ret = utils.CheckIpOrDomainValid(s)
	assert.Equal(t, ret, utils.Domain)

	s = "www.baidu.com"
	ret = utils.CheckIpOrDomainValid(s)
	assert.Equal(t, ret, utils.Domain)
}

func TestFilterIpsWithIpType(t *testing.T) {
	s := []string{"1.1.1.1", "fe80::aede:48ff:fe00:1122"}
	ret := utils.FilterIpsWithIpType(s, utils.V4)
	assert.Equal(t, ret, []string{"1.1.1.1"})

	ret = utils.FilterIpsWithIpType(s, utils.V6)
	assert.Equal(t, ret, []string{"fe80::aede:48ff:fe00:1122"})

	s = []string{"1.1.1.1"}
	ret = utils.FilterIpsWithIpType(s, utils.V6)
	assert.Equal(t, ret, []string(nil))

	s = []string{"fe80::aede:48ff:fe00:1122"}
	ret = utils.FilterIpsWithIpType(s, utils.V4)
	assert.Equal(t, ret, []string(nil))
}
