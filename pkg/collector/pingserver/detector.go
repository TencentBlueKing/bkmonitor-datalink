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

type Detector interface {
	Do()
	Result() map[string]*Response
}

type detector struct {
	addrs   []*net.IPAddr
	times   int
	timeout time.Duration
	pinger  *fastping.Pinger
	result  map[string]*Response
}

func newDetector(addrs []*net.IPAddr, times int, timeout time.Duration) Detector {
	if times <= 0 {
		times = defaultSendTimes
	}
	if timeout <= 0 {
		timeout = defaultSendTimeout
	}
	return &detector{
		pinger:  fastping.NewPinger(),
		addrs:   addrs,
		times:   times,
		timeout: timeout,
		result:  make(map[string]*Response),
	}
}

func (d *detector) OnRecv(addr *net.IPAddr, rtt time.Duration) {
	resp, ok := d.result[addr.String()]
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

func (d *detector) OnIdle() {}

func (d *detector) Do() {
	for _, addr := range d.addrs {
		d.pinger.AddIPAddr(addr)
		d.result[addr.String()] = newResponse(addr)
	}

	d.pinger.MaxRTT = d.timeout
	d.pinger.Times = d.times
	d.pinger.OnRecv = d.OnRecv
	d.pinger.OnIdle = d.OnIdle

	d.pinger.RunLoop()
	<-d.pinger.Done()
}

func (d *detector) Result() map[string]*Response {
	return d.result
}
