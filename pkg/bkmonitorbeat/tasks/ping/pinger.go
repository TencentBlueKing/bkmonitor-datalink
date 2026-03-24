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
	"encoding/binary"
	"math/rand"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// protocolIPv4ICMP is IANA ICMP IPv4
	protocolIPv4ICMP = 1
	// protocolIPv6ICMP is IANA ICMP IPv6
	protocolIPv6ICMP = 58
	// icmpHeaderSize is ICMP header size
	icmpHeaderSize = 8
	// icmpTTL is ICMP TTL
	icmpTTL = 64
	// icmpReadDeadline is ICMP read deadline
	readDeadline = 10 * time.Millisecond
	// timeoutCheckInterval is timeout check interval
	timeoutCheckInterval = 100 * time.Millisecond
	// handlerNum is handler number
	handlerNum = 5
)

// PingerTarget : ping目标配置
type PingerTarget struct {
	Target       string
	TargetType   string            // 目标类型, domain or addr
	Labels       map[string]string // 标签
	DnsCheckMode configs.CheckMode // 域名检测模式，检测所有dns ip还是取第一个, all or single
	DomainIpType configs.IPType    // 域名检测类型， 0: addr, 4: ipv4, 6: ipv6
	MaxRtt       time.Duration     // 最大rtt，超过该值则认为超时
	Times        int               // 检测次数
	Size         int               // 发送icmp包大小

	result map[string][]time.Duration
}

// GetResult : 获取结果
func (t *PingerTarget) GetResult() map[string][]time.Duration {
	return t.result
}

// pingerInstance : ping实例
type pingerInstance struct {
	ip     net.IP
	ipType string

	maxRtt time.Duration
	times  int
	size   int

	index      int
	results    []*pingerResult
	isFinished bool

	lock sync.Mutex
}

func newPingerInstance(ip net.IP, ipType string, maxRtt time.Duration, times int, size int) *pingerInstance {
	results := make([]*pingerResult, times)
	for i := 0; i < times; i++ {
		results[i] = &pingerResult{}
	}

	return &pingerInstance{
		ip:      ip,
		ipType:  ipType,
		maxRtt:  maxRtt,
		times:   times,
		size:    size,
		results: results,
	}
}

// pingerResult : ping结果
type pingerResult struct {
	SendTime time.Time
	RecvTime time.Time
	Timeout  bool
}

// IsFinished : 是否已经完成
func (p *pingerResult) IsFinished() bool {
	return !p.RecvTime.IsZero() || p.Timeout
}

// IsSent : 是否已经发送
func (p *pingerResult) IsSent() bool {
	return !p.SendTime.IsZero()
}

// RTT : 获取rtt
func (p *pingerResult) RTT() time.Duration {
	return p.RecvTime.Sub(p.SendTime)
}

// Pinger : ping 探测器
type Pinger struct {
	id int

	conn4 *icmp.PacketConn
	conn6 *icmp.PacketConn

	// 记录解析后的ip对应的目标
	haveIPv4   bool
	haveIPv6   bool
	targetToIP map[string][]string
	instances  map[string]*pingerInstance

	// 发送间隔
	sendInterval time.Duration

	// 是否特权模式，默认的ping需要root权限
	privileged bool

	// 发送队列
	sendQueue chan *pingerInstance
	// 处理队列
	replyQueue chan *pingerPacket
}

// pingerPacket : ping消息包
type pingerPacket struct {
	instance *pingerInstance
	message  []byte
	recvTime time.Time
}

// NewPinger : 创建ping探测器
func NewPinger(sendInterval time.Duration, privileged bool) *Pinger {
	return &Pinger{
		id: rand.Intn(0xffff),

		sendInterval: sendInterval,
		privileged:   privileged,

		targetToIP: make(map[string][]string),
		instances:  make(map[string]*pingerInstance),
	}
}

// Ping : ping探测
func (p *Pinger) Ping(ctx context.Context, targets []*PingerTarget) error {
	logger.Infof("start ping, id: %d , target count:%d", p.id, len(targets))

	// 解析目标
	if err := p.parseTarget(targets); err != nil {
		return err
	}

	// 没有有效ip则直接返回
	if !p.haveIPv4 && !p.haveIPv6 {
		logger.Errorf("no valid addr to ping")
		return nil
	}

	// 开始监听
	if err := p.listen(); err != nil {
		return err
	}
	defer p.close()

	wg := new(sync.WaitGroup)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 发送请求
	wg.Add(1)
	go func() {
		defer wg.Done()

		startTime := time.Now().Add(-p.sendInterval)
		for {
			select {
			case <-ctx.Done():
				logger.Info("ping done: send worker exit")
				return
			case instance := <-p.sendQueue:
				// 发送间隔控制，不能使用 ticker，因为 ticker 在时间很小的情况下存在性能问题
				now := time.Now()
				elapse := now.Sub(startTime)
				if elapse < p.sendInterval {
					time.Sleep(p.sendInterval - elapse)
				}
				startTime = now

				// 发送icmp包
				if err := p.send(instance); err != nil {
					logger.Errorf("send icmp packet failed, error:%v", err)
				}
			}
		}
	}()

	// 推送发送队列及超时检查
	wg.Add(1)
	go func() {
		defer wg.Done()

		// 推送第一次发送
		for _, instance := range p.instances {
			p.sendQueue <- instance
		}

		// 超时检查，每100ms进行一次检查
		ticker := time.NewTicker(timeoutCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Info("ping done: timeout check worker exit")
				return
			case <-ticker.C:
				allFinished := p.checkTimeout()
				if allFinished {
					logger.Info("ping done: all instance finished")
					cancel()
					return
				}
			}
		}
	}()

	// 接收icmp包
	for _, conn := range []*icmp.PacketConn{p.conn4, p.conn6} {
		if conn == nil {
			continue
		}

		wg.Add(1)
		conn := conn
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					logger.Info("ping done: receive worker exit")
					return
				default:
					if err := p.receive(conn); err != nil {
						logger.Errorf("receive icmp packet failed, error:%v", err)
					}
				}
			}
		}()
	}

	// 处理icmp响应消息
	for i := 0; i < handlerNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					logger.Info("ping done: reply worker exit")
					return
				case packet := <-p.replyQueue:
					// handle icmp response
					if err := p.handleResponse(packet); err != nil {
						logger.Errorf("handle icmp response failed, error:%v", err)
					}
				}
			}
		}()
	}

	wg.Wait()

	// 结果处理
	for _, target := range targets {
		var (
			ips []string
			ok  bool
		)

		// 判断目标类型，获取对应的ip
		if target.TargetType == "domain" {
			ips, ok = p.targetToIP[target.Target]
			if !ok {
				continue
			}
		} else {
			ips = []string{target.Target}
		}

		target.result = make(map[string][]time.Duration)
		for _, ip := range ips {
			// 初始化结果
			if _, ok := target.result[ip]; !ok {
				target.result[ip] = make([]time.Duration, target.Times)
			}

			// 获取探测结果
			instance, ok := p.instances[ip]
			if !ok {
				for i := 0; i < target.Times; i++ {
					target.result[ip][i] = -1
				}
				continue
			}

			for i, result := range instance.results {
				// 如果任务未完成或超时，则记录为-1
				if !result.IsFinished() || result.Timeout {
					target.result[ip][i] = -1
					continue
				}

				// 记录rtt
				target.result[ip][i] = result.RTT()
			}
		}

	}

	return nil
}

// listen : 启动icmp监听
func (p *Pinger) listen() error {
	var network string

	// ipv4
	if p.haveIPv4 {
		// 是否特权模式
		if p.privileged {
			network = "ip4:icmp"
		} else {
			network = "udp4"
		}

		conn, err := icmp.ListenPacket(network, "0.0.0.0")
		if err != nil {
			return err
		}

		// 设置ttl
		_ = conn.IPv4PacketConn().SetTTL(icmpTTL)
		_ = conn.IPv4PacketConn().SetControlMessage(ipv4.FlagTTL, true)

		// 设置tos
		_ = conn.IPv4PacketConn().SetTOS(0)

		// 设置接口
		_ = conn.IPv4PacketConn().SetControlMessage(ipv4.FlagInterface, true)
		p.conn4 = conn
	}

	// ipv6
	if p.haveIPv6 {
		// 是否特权模式
		if p.privileged {
			network = "ip6:ipv6-icmp"
		} else {
			network = "udp6"
		}

		conn, err := icmp.ListenPacket(network, "::")
		if err != nil {
			return err
		}

		// 设置ttl
		_ = conn.IPv6PacketConn().SetHopLimit(icmpTTL)
		_ = conn.IPv6PacketConn().SetControlMessage(ipv6.FlagHopLimit, true)

		// 设置接口
		_ = conn.IPv6PacketConn().SetControlMessage(ipv6.FlagInterface, true)
		p.conn6 = conn
	}

	return nil
}

// send : 发送icmp包
func (p *Pinger) send(instance *pingerInstance) error {
	var conn *icmp.PacketConn
	var icmpType icmp.Type
	var addr net.Addr

	// 判断ip类型
	if instance.ipType == "ipv4" {
		conn = p.conn4
		icmpType = ipv4.ICMPTypeEcho
	} else {
		conn = p.conn6
		icmpType = ipv6.ICMPTypeEchoRequest
	}

	// 是否特权模式
	if p.privileged {
		addr = &net.IPAddr{IP: instance.ip}
	} else {
		addr = &net.UDPAddr{IP: instance.ip}
	}

	// payload，填充时间戳，剩余部分填充0，保证icmp包大小
	payload := make([]byte, instance.size-icmpHeaderSize)
	binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))

	// icmp消息
	msg, err := (&icmp.Message{
		Type: icmpType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   p.id,
			Seq:  instance.index,
			Data: payload,
		},
	}).Marshal(nil)
	if err != nil {
		return err
	}

	// 记录发送时间
	result := instance.results[instance.index]

	// 发送icmp包，防止缓冲区满
	for i := 0; i < 2; i++ {
		result.SendTime = time.Now()
		if _, err = conn.WriteTo(msg, addr); err != nil {
			var netErr *net.OpError
			if errors.As(err, &netErr) && errors.Is(netErr.Err, syscall.ENOBUFS) {
				continue
			}
		}
		break
	}

	return nil
}

// receive : 接收icmp包
func (p *Pinger) receive(conn *icmp.PacketConn) error {
	reply := make([]byte, 1024)

	// 设置读取超时时间
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		return err
	}

	// 读取icmp包
	n, addr, err := conn.ReadFrom(reply)
	recvTime := time.Now()
	if err != nil {
		var netErr *net.OpError
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil
		}
		return err
	}

	// 丢弃无用包
	if addr == nil {
		return nil
	}

	var ip string
	switch obj := addr.(type) {
	case *net.IPAddr:
		ip = obj.IP.String()
	case *net.UDPAddr:
		ip = obj.IP.String()
	default:
		return nil
	}

	instance, ok := p.instances[ip]
	if !ok {
		return nil
	}

	// 推送到处理队列
	p.replyQueue <- &pingerPacket{
		message:  reply[:n],
		instance: instance,
		recvTime: recvTime,
	}

	return nil
}

// checkTimeout : 检查超时
func (p *Pinger) checkTimeout() bool {
	now := time.Now()

	// 全部的实例是否都已经完成，如果完成则返回true，后续可以退出整个ping任务
	allFinished := true

	for _, instance := range p.instances {
		func() {
			// 加读锁
			instance.lock.Lock()
			defer instance.lock.Unlock()

			// 如果该实例已经完成全部检查则跳过
			if instance.isFinished {
				return
			}

			// 超时检查
			result := instance.results[instance.index]
			if !result.IsSent() || result.IsFinished() || now.Sub(result.SendTime) < instance.maxRtt {
				// 当前的处理结果没有超时，但是也没有完成全部的发送，标记未完成，提前退出跳过后续超时处理流程
				allFinished = false
				return
			}

			// 超时
			result.Timeout = true

			// 如果发送次数小于需要发送的次数，则再次发送，否则标记完成
			if instance.index < instance.times-1 {
				// 没有完成全部的发送，标记未完成
				allFinished = false

				// 再次发送
				instance.index++
				p.sendQueue <- instance
			} else {
				// 完成
				instance.isFinished = true
			}
		}()
	}

	return allFinished
}

// handleResponse : 处理icmp响应
func (p *Pinger) handleResponse(packet *pingerPacket) error {
	var (
		icmpType  int
		echoReply *icmp.Echo
		ok        bool
	)

	instance := packet.instance

	if instance.ipType == "ipv4" {
		icmpType = protocolIPv4ICMP
	} else {
		icmpType = protocolIPv6ICMP
	}

	msg, err := icmp.ParseMessage(icmpType, packet.message)
	if err != nil {
		return err
	}

	switch msg.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
		echoReply, ok = msg.Body.(*icmp.Echo)
		if !ok {
			return errors.Errorf("invalid ICMP Echo Reply message, ip: %s, invalid body, type: %T", instance.ip, msg.Body)
		}
	default:
		return nil
	}

	// 判断seq是否合法
	index := echoReply.Seq
	if index >= instance.times {
		return nil
	}

	// 判断id是否合法
	if echoReply.ID != p.id {
		return nil
	}

	// 加写锁
	instance.lock.Lock()
	defer instance.lock.Unlock()

	result := instance.results[index]
	if result.IsFinished() || !result.IsSent() {
		return nil
	}

	// 记录接收时间
	result.RecvTime = packet.recvTime

	// 判断是否完成，如果未完成则继续发送
	if index == packet.instance.times-1 {
		instance.isFinished = true
	} else {
		instance.index++
		p.sendQueue <- instance
	}

	return nil
}

// close : 关闭icmp连接
func (p *Pinger) close() {
	if p.conn4 != nil {
		if err := p.conn4.Close(); err != nil {
			logger.Warnf("close icmp connection failed, error:%v", err)
		}

		p.conn4 = nil
	}

	if p.conn6 != nil {
		if err := p.conn6.Close(); err != nil {
			logger.Warnf("close icmp connection failed, error:%v", err)
		}

		p.conn6 = nil
	}
}

// parseTarget : 解析目标
func (p *Pinger) parseTarget(targets []*PingerTarget) error {
	var (
		ips []net.IP
		err error
	)

	for _, target := range targets {
		// 解析目标ip
		if target.TargetType == "domain" {
			ips, err = tasks.LookupIP(context.Background(), target.DomainIpType, target.Target)
			if err != nil {
				logger.Errorf("lookup domain ip failed, domain:%s, error:%v", target.Target, err)
				continue
			}
			// 如果是单个模式，只取第一个ip
			if target.DnsCheckMode == configs.CheckModeSingle {
				ips = ips[:1]
			}
		} else {
			ips = []net.IP{net.ParseIP(target.Target)}
		}

		for _, ip := range ips {
			ipStr := ip.String()

			// 判断ip类型
			ipType := "ipv4"
			if ip.To16() != nil && ip.To4() == nil {
				ipType = "ipv6"
				p.haveIPv6 = true
			} else {
				p.haveIPv4 = true
			}

			// 记录解析后的ip对应的目标
			p.targetToIP[target.Target] = append(p.targetToIP[target.Target], ipStr)

			// 初始化结果
			if _, ok := p.instances[ipStr]; !ok {
				p.instances[ipStr] = newPingerInstance(ip, ipType, target.MaxRtt, target.Times, target.Size)
			}
		}
	}

	// 初始化发送和接收队列
	queueLength := len(p.instances)
	p.sendQueue = make(chan *pingerInstance, queueLength)
	p.replyQueue = make(chan *pingerPacket, queueLength)
	return nil
}
