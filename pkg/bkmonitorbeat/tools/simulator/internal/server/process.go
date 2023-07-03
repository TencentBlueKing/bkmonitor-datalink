// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package server

import (
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

func serveProcess(tcpPort int, udpPort int) error {
	tcpAddr := fmt.Sprintf(":%d", tcpPort)
	udpAddr := fmt.Sprintf(":%d", udpPort)
	type listenInfo struct {
		addr       string
		listenFunc func(addr string) (io.Closer, error)
		l          io.Closer
	}
	m := map[string]*listenInfo{
		"tcp": {tcpAddr, func(addr string) (io.Closer, error) {
			return net.Listen("tcp", addr)
		}, nil},
		"udp": {udpAddr, func(addr string) (io.Closer, error) {
			return net.ListenPacket("udp", addr)
		}, nil},
	}
	var openNetwork, closeNetwork string
	var err error
	for {
		if isNormal() {
			openNetwork = "tcp"
			closeNetwork = "udp"
		} else {
			openNetwork = "udp"
			closeNetwork = "tcp"
		}
		if info, ok := m[openNetwork]; ok && info.l == nil {
			log.Println("open", openNetwork, info.addr)
			info.l, err = info.listenFunc(info.addr)
			if err != nil {
				log.Println("error open network", info.addr, err)
			}
		}
		if info, ok := m[closeNetwork]; ok && info.l != nil {
			log.Println("close", openNetwork, info.addr)
			_ = info.l.Close()
			info.l = nil
		}
		time.Sleep(time.Second)
	}
}
