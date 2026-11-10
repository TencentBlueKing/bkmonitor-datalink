// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");

package active_profile

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
)

// stubLocalAddrs 把本机网卡地址与主机名解析替换为固定结果，测试结束后恢复。
func stubLocalAddrs(t *testing.T, interfaces []string, hosts map[string][]string) {
	t.Helper()
	originalInterfaces, originalLookup := listInterfaceAddrs, lookupEndpointHost
	t.Cleanup(func() {
		listInterfaceAddrs, lookupEndpointHost = originalInterfaces, originalLookup
	})
	listInterfaceAddrs = func() ([]net.Addr, error) {
		addrs := make([]net.Addr, 0, len(interfaces))
		for _, cidr := range interfaces {
			ip, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				t.Fatal(err)
			}
			ipNet.IP = ip
			addrs = append(addrs, ipNet)
		}
		return addrs, nil
	}
	lookupEndpointHost = func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
		values, exists := hosts[host]
		if !exists {
			return nil, errors.New("no such host")
		}
		addrs := make([]netip.Addr, 0, len(values))
		for _, value := range values {
			addrs = append(addrs, netip.MustParseAddr(value))
		}
		return addrs, nil
	}
}

// 只有 endpoint 指向本机时才允许反查；主机名必须全部解析到本机，混入外部地址时
// 连到哪个不可预知，按不在本机处理。
func TestLocalEndpointAddrs(t *testing.T) {
	stubLocalAddrs(t,
		[]string{"10.0.0.5/24", "fe80::1/64"},
		map[string][]string{
			"localhost": {"127.0.0.1", "::1"},
			"self":      {"10.0.0.5"},
			"mixed":     {"10.0.0.5", "10.0.0.9"},
			"remote":    {"10.0.0.9"},
		},
	)
	cases := []struct {
		address string
		local   bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"::", true},
		{"10.0.0.5", true},
		{"::ffff:10.0.0.5", true},
		{"fe80::1%eth0", true},
		{"localhost", true},
		{"self", true},
		{"10.0.0.9", false},
		{"192.0.2.1", false},
		{"mixed", false},
		{"remote", false},
		{"unresolvable", false},
	}
	for _, c := range cases {
		_, local := localEndpointAddrs(context.Background(), c.address)
		if local != c.local {
			t.Errorf("localEndpointAddrs(%q) local=%v, want %v", c.address, local, c.local)
		}
	}
}

// 同一端口绑在不同本机地址上时，只有能接收发往 endpoint 地址连接的监听 socket 才算。
func TestListenerServes(t *testing.T) {
	addrs := func(values ...string) []netip.Addr {
		result := make([]netip.Addr, 0, len(values))
		for _, value := range values {
			result = append(result, netip.MustParseAddr(value))
		}
		return result
	}
	cases := []struct {
		name     string
		listenIP string
		targets  []netip.Addr
		serves   bool
	}{
		{"same loopback", "127.0.0.1", addrs("127.0.0.1"), true},
		{"ipv4 wildcard serves ipv4", "0.0.0.0", addrs("10.0.0.5"), true},
		{"ipv4 wildcard does not serve ipv6", "0.0.0.0", addrs("::1"), false},
		{"ipv6 wildcard is dual stack", "::", addrs("127.0.0.1"), true},
		{"loopback listener does not serve nic address", "127.0.0.1", addrs("10.0.0.5"), false},
		{"nic listener does not serve loopback", "10.0.0.5", addrs("127.0.0.1"), false},
		{"other nic address", "10.0.0.6", addrs("10.0.0.5"), false},
		{"mapped listener address", "::ffff:127.0.0.1", addrs("127.0.0.1"), true},
		{"any resolved address", "::1", addrs("127.0.0.1", "::1"), true},
		{"unspecified endpoint matches any listener", "10.0.0.5", addrs("0.0.0.0"), true},
		{"unparsable listener address", "", addrs("127.0.0.1"), false},
	}
	for _, c := range cases {
		if serves := listenerServes(c.listenIP, c.targets); serves != c.serves {
			t.Errorf("%s: listenerServes(%q, %v)=%v, want %v", c.name, c.listenIP, c.targets, serves, c.serves)
		}
	}
}

// 跨主机采集时本机同端口的监听进程与被采集进程无关，不能被标进产物。用本测试进程
// 自己的监听端口验证：endpoint 指向本机时反查到自己，指向外部地址时一律为空。
func TestResolveGoEndpointProcessOnlyOnLocalEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	if processes := resolveGoEndpointProcess(context.Background(), "192.0.2.1", port); processes != nil {
		t.Fatalf("remote endpoint resolved to %v, want no process", processes)
	}

	// 其他平台的 socket 枚举依赖外部命令或额外权限，只在 Linux 上校验本机反查结果。
	if runtime.GOOS != "linux" {
		return
	}
	processes := resolveGoEndpointProcess(context.Background(), "127.0.0.1", port)
	if len(processes) != 1 || processes[0].PID != int32(os.Getpid()) {
		t.Fatalf("local endpoint resolved to %v, want pid %d", processes, os.Getpid())
	}
	// 监听只绑在回环地址上，发往本机网卡地址的连接到不了它。
	for _, address := range nonLoopbackLocalAddrs(t) {
		if processes := resolveGoEndpointProcess(context.Background(), address, port); processes != nil {
			t.Fatalf("endpoint %s resolved to loopback-only listener %v", address, processes)
		}
	}
}

func nonLoopbackLocalAddrs(t *testing.T) []string {
	t.Helper()
	interfaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, interfaceAddr := range interfaceAddrs {
		addr, ok := interfaceIP(interfaceAddr)
		if ok && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() {
			result = append(result, addr.String())
		}
	}
	return result
}
