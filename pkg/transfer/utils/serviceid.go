// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"fmt"
	"hash/fnv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// 获取serviceID
func GetServiceID(conf define.Configuration) string {
	address := conf.GetString(define.ConfHost)
	port := conf.GetInt(define.ConfPort)

	hash := fnv.New32a()
	_, err := hash.Write([]byte(fmt.Sprintf("%s:%d", address, port)))
	if err != nil {
		panic(err)
	}

	return fmt.Sprintf("%d", hash.Sum32())
}
