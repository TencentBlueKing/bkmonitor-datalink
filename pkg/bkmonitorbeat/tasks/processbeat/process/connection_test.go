// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package process

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

type testArgs struct {
	conf    configs.ProcessbeatPortConfig
	sockets []FileSocket
	pidcnt  int
	want    []PortStat
}

func TestCalcPortStat(t *testing.T) {
	const name = "java"
	const display = "java-test"

	t.Run("tcp listen", func(t *testing.T) {
		args := testArgs{
			conf: configs.ProcessbeatPortConfig{
				Name:        name,
				DisplayName: display,
				Protocol:    ProtocolTCP,
				Ports:       []uint16{100, 200},
				BindInfoList: []configs.ProcessbeatBindInfo{
					{
						IP:       "0.0.0.0",
						Ports:    []uint16{400, 300, 200},
						Protocol: ProtocolTCP,
					},
				},
			},
			sockets: []FileSocket{
				{
					Protocol:  ProtocolTCP,
					ConnLaddr: "127.0.0.1",
					ConnLport: 100,
				},
				{
					Protocol:  ProtocolTCP,
					ConnLaddr: "10.0.0.1",
					ConnLport: 200,
				},
				{
					Protocol:  ProtocolTCP,
					ConnLaddr: "::", // 存在 [::] 则一定会存在 [0.0.0.0]
					ConnLport: 300,
				},
				{
					Protocol:  ProtocolTCP,
					ConnLaddr: "0.0.0.0",
					ConnLport: 300,
				},
			},
			pidcnt: 1,
			want: []PortStat{
				{
					ProcName:          name,
					Status:            1,
					Protocol:          ProtocolTCP,
					Listen:            []uint16{300},
					NonListen:         []uint16{400},
					NotAccurateListen: []string{"10.0.0.1:200"},
					BindIP:            "0.0.0.0",
					ParamRegex:        "",
					DisplayName:       display,
					PortHealthy:       0,
				},
			},
		}

		got := calcPortStat(args.conf, args.sockets, args.pidcnt)
		assert.Equal(t, args.want, got)
	})

	t.Run("tcp6 listen", func(t *testing.T) {
		args := testArgs{
			conf: configs.ProcessbeatPortConfig{
				Name:        name,
				DisplayName: display,
				Protocol:    ProtocolTCP,
				Ports:       []uint16{100, 200},
				BindInfoList: []configs.ProcessbeatBindInfo{
					{
						IP:       "fe80::5054:ff:fe1e:f927",
						Ports:    []uint16{400, 300, 200},
						Protocol: ProtocolTCP6,
					},
				},
			},
			sockets: []FileSocket{
				{
					Protocol:  ProtocolTCP6,
					ConnLaddr: "fe80::5054:ff:fe1e:f927",
					ConnLport: 300,
				},
			},
			pidcnt: 1,
			want: []PortStat{
				{
					ProcName:          name,
					Status:            1,
					Protocol:          ProtocolTCP6,
					Listen:            []uint16{300},
					NonListen:         []uint16{200, 400},
					NotAccurateListen: []string{},
					BindIP:            "fe80::5054:ff:fe1e:f927",
					DisplayName:       display,
					PortHealthy:       0,
				},
			},
		}

		got := calcPortStat(args.conf, args.sockets, args.pidcnt)
		assert.Equal(t, args.want, got)
	})

	t.Run("udp6 listen", func(t *testing.T) {
		args := testArgs{
			conf: configs.ProcessbeatPortConfig{
				Name:        name,
				DisplayName: display,
				Protocol:    ProtocolUDP,
				Ports:       []uint16{100, 200},
				BindInfoList: []configs.ProcessbeatBindInfo{
					{
						IP:       "fe80::5054:ff:fe1e:f927",
						Ports:    []uint16{400, 300, 200},
						Protocol: ProtocolUDP6,
					},
				},
			},
			sockets: []FileSocket{
				{
					Protocol:  ProtocolUDP6,
					ConnLaddr: "fe80::5054:ff:fe1e:f928",
					ConnLport: 300,
				},
			},
			pidcnt: 1,
			want: []PortStat{
				{
					ProcName:          name,
					Status:            1,
					Protocol:          ProtocolUDP6,
					Listen:            []uint16{},
					NonListen:         []uint16{200, 400},
					NotAccurateListen: []string{"fe80::5054:ff:fe1e:f928:300"},
					BindIP:            "fe80::5054:ff:fe1e:f927",
					DisplayName:       display,
					PortHealthy:       0,
				},
			},
		}

		got := calcPortStat(args.conf, args.sockets, args.pidcnt)
		assert.Equal(t, args.want, got)
	})
}
