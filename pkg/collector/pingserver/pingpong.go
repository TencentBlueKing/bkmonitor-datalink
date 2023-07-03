// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import (
	"net"
	"time"

	"github.com/liuwenping/go-fastping"
)

const (
	minDuration time.Duration = -1 << 63
	maxDuration time.Duration = 1<<63 - 1

	defaultSendTimeout = 3 * time.Second
	defaultSendTimes   = 1
)

type Response struct {
	Addr      *net.IPAddr
	MinRtt    time.Duration
	MaxRtt    time.Duration
	TotalRtt  time.Duration
	RecvCount int
}

func newResponse(addr *net.IPAddr) *Response {
	return &Response{
		Addr:   addr,
		MinRtt: maxDuration,
		MaxRtt: minDuration,
	}
}

type PingPong struct {
	addrs   []*net.IPAddr
	times   int
	timeout time.Duration
	pinger  *fastping.Pinger
	result  map[string]*Response
}

func NewPingPong(addrs []*net.IPAddr, times int, timeout time.Duration) *PingPong {
	if times <= 0 {
		times = defaultSendTimes
	}
	if timeout <= 0 {
		timeout = defaultSendTimeout
	}
	return &PingPong{
		pinger:  fastping.NewPinger(),
		addrs:   addrs,
		times:   times,
		timeout: timeout,
		result:  make(map[string]*Response),
	}
}

func (pp *PingPong) OnRecv(addr *net.IPAddr, rtt time.Duration) {
	resp, ok := pp.result[addr.String()]
	if !ok {
		return
	}

	if rtt > resp.MaxRtt {
		resp.MaxRtt = rtt
	}
	if rtt < resp.MinRtt {
		resp.MinRtt = rtt
	}
	resp.TotalRtt += rtt
	resp.RecvCount++
}

func (pp *PingPong) OnIdle() {}

func (pp *PingPong) Do() {
	for _, addr := range pp.addrs {
		pp.pinger.AddIPAddr(addr)
		pp.result[addr.String()] = newResponse(addr)
	}

	pp.pinger.MaxRTT = pp.timeout
	pp.pinger.Times = pp.times
	pp.pinger.OnRecv = pp.OnRecv
	pp.pinger.OnIdle = pp.OnIdle

	pp.pinger.RunLoop()
	<-pp.pinger.Done()
}

func (pp *PingPong) Result() map[string]*Response {
	return pp.result
}
