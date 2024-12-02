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
	once          sync.Once
	localIP       string
	localHostName string
)

// GetLocalHost 获取本机名称和 IP
func GetLocalHost() (string, string) {
	once.Do(func() {
		localHostName, _ = os.Hostname()

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

					if ip == nil || ip.IsLoopback() {
						continue
					}
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
