// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proccustom

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/processbeat/process"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type portEvent struct {
	conns    []process.FileSocket
	procName string
	username string
	pid      int32
	dims     map[string]string
	tags     map[string]string
	labels   []map[string]string
}

func (e portEvent) getDims() map[string]string {
	ret := make(map[string]string)
	for k, v := range e.tags {
		ret[k] = v
	}
	for k, v := range e.dims {
		ret[k] = v
	}
	return ret
}

func (e portEvent) AsMapStr() []common.MapStr {
	ret := make([]common.MapStr, 0)
	connStates := e.alive()

	for _, conn := range connStates {
		for _, label := range e.labels {
			dimensions := make(common.MapStr)
			for k, v := range e.getDims() {
				dimensions[k] = v
			}
			dimensions["pid"] = fmt.Sprintf("%d", e.pid)
			dimensions["process_name"] = e.procName
			dimensions["process_username"] = e.username
			dimensions["listen_address"] = conn.Address
			dimensions["listen_port"] = conn.Port

			cloudID, ok := label["bk_target_cloud_id"]
			if !ok {
				continue
			}
			ip, ok := label["bk_target_ip"]
			if !ok {
				continue
			}

			for k, v := range label {
				dimensions[k] = v
			}

			ret = append(ret, common.MapStr{
				"timestamp": time.Now().Unix(),
				"target":    fmt.Sprintf("%s:%s", cloudID, ip),
				"dimension": dimensions,
				"metrics": common.MapStr{
					"alive": conn.Alive,
				},
			})
		}
	}

	return ret
}

type ConnState struct {
	Address string
	Port    string
	Alive   int
}

func (e portEvent) alive() []ConnState {
	var ret []ConnState

	for _, conn := range e.conns {
		logger.Debugf("%d(%s) handle FileSocket: %+v", e.pid, e.procName, conn)
		if conn.Status != "LISTEN" && conn.Status != "0A" {
			continue
		}
		var alive int
		if err := e.touch(conn.Saddr, int(conn.Sport)); err != nil {
			logger.Errorf("touch addr %s port %d failed", conn.Saddr, conn.Sport)
			alive = 0
		} else {
			alive = 1
		}
		c := ConnState{
			Address: conn.Saddr,
			Port:    fmt.Sprintf("%d", conn.Sport),
			Alive:   alive,
		}
		logger.Debugf("%d(%s) got connState: %+v", e.pid, e.procName, c)
		ret = append(ret, c)
	}

	// 什么都没有的话上报为空
	if len(ret) == 0 {
		return []ConnState{{}}
	}

	return ret
}

func (e portEvent) touch(ip string, port int) error {
	ipStruct := net.ParseIP(ip)
	if ipStruct.IsUnspecified() {
		t := configs.IPAuto
		if ipStruct.Equal(net.IPv4zero) {
			t = configs.IPv4
		} else if ipStruct.Equal(net.IPv6zero) {
			t = configs.IPv6
		}
		ips := tasks.DefaultIPs(t)
		if len(ips) == 0 {
			logger.Error("no default ips")
			return errors.New("no default ips")
		}
		ip = ips[0]
		logger.Infof("%d(%s) touch using ip %s", e.pid, e.procName, ip)
	}
	conn, err := net.Dial("tcp", fmt.Sprintf("[%s]:%d", ip, port))
	if err != nil {
		return err
	}

	defer conn.Close()
	return nil
}
