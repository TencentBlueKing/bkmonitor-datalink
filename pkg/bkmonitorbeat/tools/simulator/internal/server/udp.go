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
	"bytes"
	"fmt"
	"log"
	"net"
)

func serveUDP(port int, response string) error {
	addr := fmt.Sprintf(":%d", port)
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Fatalln(err)
	}
	defer pc.Close()
	for {
		buf := make([]byte, 1024)
		var remoteAddr net.Addr
		_, remoteAddr, err = pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		go func(s net.PacketConn, a net.Addr) {
			bs := bytes.NewBuffer([]byte{})
			handleResponse(bs, response)
			s.WriteTo(bs.Bytes(), a)
		}(pc, remoteAddr)
	}
}
