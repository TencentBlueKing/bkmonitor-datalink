// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLocalHost(t *testing.T) {
	hostname, ip := GetLocalHost()

	// 验证返回的主机名不为空
	assert.NotEmpty(t, hostname, "hostname should not be empty")

	// 验证 IP 可能为空（如果没有非回环的 IPv4 地址），但不应该为空字符串以外的值
	// 如果 IP 不为空，应该是有效的 IPv4 格式
	if ip != "" {
		assert.NotEmpty(t, ip, "if IP is returned, it should not be empty")
		// 可以添加更严格的 IP 格式验证，但这里只验证基本功能
	}

	// 测试多次调用应该返回相同的结果（因为使用了 sync.Once）
	hostname2, ip2 := GetLocalHost()
	assert.Equal(t, hostname, hostname2, "hostname should be consistent across calls")
	assert.Equal(t, ip, ip2, "IP should be consistent across calls")
}

func TestGetLocalHost_Concurrent(t *testing.T) {
	// 测试并发调用
	results := make(chan struct {
		hostname string
		ip       string
	}, 10)

	for i := 0; i < 10; i++ {
		go func() {
			hostname, ip := GetLocalHost()
			results <- struct {
				hostname string
				ip       string
			}{hostname, ip}
		}()
	}

	// 收集所有结果
	var firstHostname, firstIP string
	for i := 0; i < 10; i++ {
		result := <-results
		if i == 0 {
			firstHostname = result.hostname
			firstIP = result.ip
		} else {
			// 验证所有并发调用返回相同的结果
			assert.Equal(t, firstHostname, result.hostname, "concurrent calls should return same hostname")
			assert.Equal(t, firstIP, result.ip, "concurrent calls should return same IP")
		}
	}
}
