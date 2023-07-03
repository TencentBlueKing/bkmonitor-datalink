// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
	"hash/fnv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
)

// 获取serviceID
func GetServiceID() string {
	host := config.Configuration.Http.Host
	port := config.Configuration.Http.Port

	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf("%s:%d", host, port)))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%d", config.Configuration.Consul.ServiceName, hash.Sum32())
}
