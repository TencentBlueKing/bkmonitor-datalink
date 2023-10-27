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
	"bytes"
	"fmt"
	"hash/fnv"
	"net"
	"os"
)

var (
	Version   string
	BuildHash string
)

var ProcessID string

var ServiceID string

func initProcessID() {
	interfaces, err := net.Interfaces()
	if err != nil {
		panic(fmt.Errorf("get mac address error: %v", err))
	}
	var addr string
	for _, i := range interfaces {
		if i.Flags&net.FlagUp != 0 && !bytes.Equal(i.HardwareAddr, nil) {
			addr = i.HardwareAddr.String()
			break
		}
	}
	if addr == "" {
		panic(fmt.Errorf("search mac address failed"))
	}

	hash := fnv.New32a()
	_, err = hash.Write([]byte(fmt.Sprintf("%s-%d", addr, os.Getpid())))
	if err != nil {
		panic(fmt.Errorf("calc client id failed: %v", err))
	}
	ProcessID = fmt.Sprintf("ingester-%d", hash.Sum32())
}

func init() {
	initProcessID()
}
