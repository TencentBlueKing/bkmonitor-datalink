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
	"net"
	"os"
	"sync"
)

var (
	once          sync.Once // 确保本地主机信息只初始化一次
	localIP       string    // 本地 IP 地址（IPv4）
	localHostName string    // 本地主机名
)

// GetLocalHost 获取本机名称和 IP 地址
// 使用 sync.Once 确保只初始化一次，提高性能
// 返回:
//   - string: 主机名，如果获取失败可能为空
//   - string: IPv4 地址，如果未找到非回环的 IPv4 地址则为空字符串
//
// 注意: 该方法会遍历所有网络接口，查找第一个非回环的 IPv4 地址
func GetLocalHost() (string, string) {
	once.Do(func() {
		// 获取主机名
		localHostName, _ = os.Hostname()

		// 获取本地 IP 地址
		func() {
			interfaces, _ := net.Interfaces()
			for _, i := range interfaces {
				adders, err := i.Addrs()
				if err != nil {
					continue
				}

				for _, addr := range adders {
					var ip net.IP
					switch v := addr.(type) {
					case *net.IPNet:
						ip = v.IP
					case *net.IPAddr:
						ip = v.IP
					}

					// 跳过回环地址和空 IP
					if ip == nil || ip.IsLoopback() {
						continue
					}
					// 只使用 IPv4 地址
					ip = ip.To4()
					if ip == nil {
						continue
					}
					localIP = ip.String()
					return
				}
			}
		}()
	})
	return localHostName, localIP
}
