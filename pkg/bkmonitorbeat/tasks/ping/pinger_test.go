// MIT License

// Copyright (c) 2021~2024 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package ping

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPingerListen(t *testing.T) {
	pinger := NewPinger(time.Millisecond*100, false)

	pinger.haveIPv4 = true
	pinger.haveIPv6 = true

	err := pinger.listen()
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}

	assert.NotNil(t, pinger.conn4)
	assert.Equal(t, "udp", pinger.conn4.LocalAddr().Network())
	assert.NotNil(t, pinger.conn6)
	assert.Equal(t, "udp", pinger.conn6.LocalAddr().Network())

	pinger.close()

	assert.Nil(t, pinger.conn4)
	assert.Nil(t, pinger.conn6)
}

func TestPingerParseTarget(t *testing.T) {
	pinger := NewPinger(time.Millisecond*100, false)

	targets := []*PingerTarget{
		{
			Target:       "127.0.0.1",
			TargetType:   "ip",
			DomainIpType: 0,
			DnsCheckMode: "all",
			MaxRtt:       time.Second,
			Times:        3,
			Size:         65,
		},
		{
			Target:       "qq.com",
			TargetType:   "domain",
			DomainIpType: 0,
			DnsCheckMode: "all",
			MaxRtt:       time.Second,
			Times:        3,
			Size:         65,
		},
		{
			Target:       "www.qq.com",
			TargetType:   "domain",
			DomainIpType: 0,
			DnsCheckMode: "single",
			MaxRtt:       time.Second,
			Times:        3,
			Size:         65,
		},
	}

	err := pinger.parseTarget(targets)
	if err != nil {
		t.Fatalf("parseTarget error: %v", err)
	}

	assert.GreaterOrEqual(t, len(pinger.instances), 3)
	assert.Len(t, pinger.targetToIP, 3)
}

func TestPingerSend(t *testing.T) {
	pinger := NewPinger(time.Millisecond*100, false)

	ip := "127.0.0.1"
	targets := []*PingerTarget{
		{
			Target:       ip,
			TargetType:   "ip",
			DomainIpType: 0,
			DnsCheckMode: "all",
			MaxRtt:       time.Second,
			Times:        3,
			Size:         65,
		},
	}

	err := pinger.parseTarget(targets)
	if err != nil {
		t.Fatalf("parseTarget error: %v", err)
	}

	err = pinger.listen()
	defer pinger.close()
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}

	assert.False(t, pinger.haveIPv6)
	assert.Nil(t, pinger.conn6)

	instance := pinger.instances[ip]

	err = pinger.send(instance)
	if err != nil {
		t.Fatalf("send error: %v", err)
	}

	assert.NotZero(t, instance.results[0].SendTime)
}

func TestPingerPing(t *testing.T) {
	pinger := NewPinger(time.Millisecond*100, false)

	ip := "www.baidu.com"
	targets := []*PingerTarget{
		{
			Target:       ip,
			TargetType:   "domain",
			DomainIpType: 4,
			DnsCheckMode: "all",
			MaxRtt:       time.Second,
			Times:        3,
			Size:         65,
		},
	}

	ctx := context.Background()
	err := pinger.Ping(ctx, targets)
	if err != nil {
		t.Fatalf("ping error: %v", err)
	}

	target := targets[0]
	for ipStr, result := range target.GetResult() {
		t.Logf("ip: %s result: %v", ipStr, result)
		for _, rtt := range result {
			assert.Greater(t, rtt, time.Duration(0))
		}
	}
}

func TestPingerTimeout(t *testing.T) {
	pinger := NewPinger(time.Millisecond*100, false)

	ip := "127.0.0.1"
	targets := []*PingerTarget{
		{
			Target:       ip,
			TargetType:   "ip",
			DomainIpType: 0,
			DnsCheckMode: "all",
			MaxRtt:       time.Millisecond * 100,
			Times:        3,
			Size:         65,
		},
	}

	err := pinger.parseTarget(targets)
	if err != nil {
		t.Fatalf("parseTarget error: %v", err)
	}

	// 预留足够的长度防止阻塞
	pinger.sendQueue = make(chan *pingerInstance, 3)

	err = pinger.listen()
	defer pinger.close()
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}

	instance := pinger.instances[ip]

	for _, result := range instance.results {
		result.SendTime = time.Now().Add(-110 * time.Millisecond)
	}

	for index, result := range instance.results {
		allFinished := pinger.checkTimeout()

		assert.True(t, result.Timeout)
		assert.Equal(t, index == len(instance.results)-1, allFinished)
	}

	assert.Len(t, pinger.sendQueue, 2)
}
