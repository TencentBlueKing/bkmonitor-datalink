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
					Protocol: ProtocolTCP,
					Saddr:    "127.0.0.1",
					Sport:    100,
				},
				{
					Protocol: ProtocolTCP,
					Saddr:    "127.0.0.1",
					Sport:    200,
				},
				{
					Protocol: ProtocolTCP,
					Saddr:    "::", // 存在 [::] 则一定会存在 [0.0.0.0]
					Sport:    300,
				},
				{
					Protocol: ProtocolTCP,
					Saddr:    "0.0.0.0",
					Sport:    300,
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
					NotAccurateListen: []string{"127.0.0.1:200"},
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
					Protocol: ProtocolTCP6,
					Saddr:    "fe80::5054:ff:fe1e:f927",
					Sport:    300,
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
					Protocol: ProtocolUDP6,
					Saddr:    "fe80::5054:ff:fe1e:f928",
					Sport:    300,
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

func TestMergeFileSockets(t *testing.T) {
	fs1 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    101,
		},
	}

	fs2 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    103,
		},
	}

	detector := StdDetector{}
	ret := detector.mergeFileSockets(fs1, fs2)
	fs3 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    101,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    103,
		},
	}
	assert.Equal(t, fs3, ret)
}

func TestMergePidSocket(t *testing.T) {
	fs1 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    101,
		},
	}
	fs2 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.2",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.2",
			Sport:    101,
		},
	}
	fs3 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.1",
			Sport:    101,
		},
	}
	fs4 := []FileSocket{
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.4",
			Sport:    100,
		},
		{
			Protocol: ProtocolTCP,
			Saddr:    "127.0.0.4",
			Sport:    101,
		},
	}

	tcp1 := map[int32][]FileSocket{
		1: fs1,
		2: fs2,
	}
	tcp2 := map[int32][]FileSocket{
		1: fs3,
		2: fs4,
	}

	detector := StdDetector{}
	ret := detector.mergePidSockets(PidSockets{TCP: tcp1}, PidSockets{TCP: tcp2})

	excepted := NewPidSockets()
	excepted.TCP = map[int32][]FileSocket{
		1: {
			{
				Protocol: ProtocolTCP,
				Saddr:    "127.0.0.1",
				Sport:    100,
			},
			{
				Protocol: ProtocolTCP,
				Saddr:    "127.0.0.1",
				Sport:    101,
			},
		},
		2: {
			{
				Protocol: ProtocolTCP,
				Saddr:    "127.0.0.2",
				Sport:    100,
			},
			{
				Protocol: ProtocolTCP,
				Saddr:    "127.0.0.2",
				Sport:    101,
			},
			{
				Protocol: ProtocolTCP,
				Saddr:    "127.0.0.4",
				Sport:    100,
			},
			{
				Protocol: ProtocolTCP,
				Saddr:    "127.0.0.4",
				Sport:    101,
			},
		},
	}
	assert.Equal(t, excepted, ret)
}
