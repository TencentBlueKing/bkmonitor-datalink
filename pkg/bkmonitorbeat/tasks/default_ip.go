// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"context"
	"net"
	"net/url"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type HostInfo struct {
	Host  string
	Ips   []string
	Errno define.BeatErrorCode
}

func filterIpsWithDomain(domain string, ipList []string, dnsMode configs.CheckMode, ipType configs.IPType) ([]string, *HostInfo) {
	ipListTmp := ipList
	var hostsInfo *HostInfo
	if ipType != configs.IPAuto {
		ipListTmp = utils.FilterIpsWithIpType(ipList, utils.DomainType(ipType))
		if len(ipListTmp) == 0 {
			if ipType == configs.IPv4 {
				hostsInfo = &HostInfo{
					Host:  domain,
					Ips:   nil,
					Errno: define.BeatErrCodeResponseNotFindIpv4,
				}
			}
			if ipType == configs.IPv6 {
				hostsInfo = &HostInfo{
					Host:  domain,
					Ips:   nil,
					Errno: define.BeatErrCodeResponseNotFindIpv6,
				}
			}
		}
	}

	if dnsMode == configs.CheckModeSingle {
		ipListTmp = ipListTmp[:1]
	}
	return ipListTmp, hostsInfo
}

func GetHostsInfo(ctx context.Context, hosts []string, dnsMode configs.CheckMode, ipType configs.IPType, protocol configs.ProtocolType) []HostInfo {
	var hostsInfo []HostInfo
	for _, host := range hosts {
		if host == "" {
			continue
		}
		hostSrc := host
		var ipList []string
		if protocol == configs.Http {
			u, err := url.Parse(host)
			if err != nil {
				hostsInfo = append(hostsInfo, HostInfo{
					Host:  hostSrc,
					Ips:   nil,
					Errno: define.BeatErrCodeResponseParseUrlErr,
				})
				continue
			}
			host = u.Host
		}
		hostTmp, _, err := net.SplitHostPort(host)
		if err == nil && hostTmp != "" {
			host = hostTmp
		}
		// 判断host是ip还是域名
		if utils.CheckIpOrDomainValid(host) == utils.Domain {
			// host为域名 解析域名获取ip列表
			ips, err := LookupIP(ctx, ipType, host)
			if err != nil {
				// DNS解析失败
				hostsInfo = append(hostsInfo, HostInfo{
					Host:  hostSrc,
					Ips:   nil,
					Errno: define.BeatErrCodeDNSResolveError,
				})
				continue
			}
			for _, v := range ips {
				ipList = append(ipList, v.String())
			}
			ipListTmp, hostsInfoTmp := filterIpsWithDomain(host, ipList, dnsMode, ipType)
			if ipListTmp != nil {
				ipList = ipListTmp
			}
			if hostsInfoTmp != nil {
				hostsInfo = append(hostsInfo, *hostsInfoTmp)
			}
		} else {
			// host为纯ip
			ipList = append(ipList, host)
		}

		hostsInfo = append(hostsInfo, HostInfo{
			Host:  hostSrc,
			Ips:   ipList,
			Errno: define.BeatErrCodeOK,
		})
	}
	return hostsInfo
}

// getDefaultIP 获取本机所有IP
func getDefaultIP() []net.IP {
	// 获取网卡列表
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return []net.IP{}
	}
	ips := make([]net.IP, 0)
	for _, addr := range addrs {
		// 去除掩码
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		if ip == nil {
			continue
		}
		// 忽略无效IP
		if !ip.IsGlobalUnicast() {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}

var globalIPv4s []string // 存储本机ipv4列表

var globalIPv6s []string // 存储本机ipv6列表

var globalIPOnce sync.Once // 本机ip信息初始化逻辑单次执行控制

// DefaultIPs 按照ip类型获取默认IP
func DefaultIPs(t configs.IPType) []string {
	// 初始化保存到全局变量
	if globalIPv4s == nil {
		globalIPOnce.Do(func() {
			ips := getDefaultIP()
			globalIPv4s = make([]string, 0)
			globalIPv6s = make([]string, 0)
			for _, ip := range ips {
				// 判断ip类型，分别存储到全局ipv4和ipv6列表
				if ip.To4() != nil {
					globalIPv4s = append(globalIPv4s, ip.String())
				} else {
					globalIPv6s = append(globalIPv6s, ip.String())
				}
			}
		})
	}

	// 按照ip类型过滤
	var ips []string
	switch t {
	case configs.IPAuto: // 所有类型
		ips = append(ips, globalIPv4s...)
		ips = append(ips, globalIPv6s...)
	case configs.IPv4:
		ips = globalIPv4s
	case configs.IPv6:
		ips = globalIPv6s
	}
	return ips
}

// GetListeningIPs 实际监听所有IP
func GetListeningIPs(addr string) []string {
	// '*' 表示是以类似 [:8080] 的方式监听的
	// 取 ipv6 [::] 以及 ipv4 [0.0.0.0]
	if addr == "*" || addr == "::" {
		return []string{net.IPv4zero.String(), net.IPv6zero.String()}
	}

	ip := net.ParseIP(addr)
	unique := map[string]struct{}{
		ip.String(): {},
	}
	// 尝试 ipv6 转 ipv4
	if len(ip) == net.IPv6len && ip.To4() != nil {
		unique[ip.To4().String()] = struct{}{}
	}

	return Map2slice(unique)
}

func Map2slice(m map[string]struct{}) []string {
	var lst []string
	for s := range m {
		lst = append(lst, s)
	}
	return lst
}

func UniqueSlice(lst []string) []string {
	set := make(map[string]struct{})
	for _, s := range lst {
		set[s] = struct{}{}
	}
	return Map2slice(set)
}

// LookupIP 按照类型解析域名为ip列表
var LookupIP = func(ctx context.Context, t configs.IPType, domain string) ([]net.IP, error) {
	// 选择解析ip类型
	var network string
	switch t {
	case configs.IPv4:
		network = "ip4"
	case configs.IPv6:
		network = "ip6"
	default:
		network = "ip"
	}
	// 判断addr是否为有效的ipv4 ipv6 或者域名
	ret := utils.CheckIpOrDomainValid(domain)
	if ret == utils.Domain {
		network = "ip"
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, network, domain)
	if err != nil {
		return nil, err
	}
	// 过滤ip
	resultIPs := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip == nil {
			// 部分情况可能无报错返回空ip，需要忽略
			logger.Debugf("ips is empty")
			continue
		}
		// LookupIP返回的都为IPv6len格式，将ipv4转为IPv4len格式
		if p4 := ip.To4(); p4 != nil {
			ip = p4
		}
		resultIPs = append(resultIPs, ip)
	}
	// 过滤后为空
	if len(resultIPs) == 0 {
		return nil, &net.AddrError{
			Err:  "no address found",
			Addr: domain,
		}
	}
	return resultIPs, nil
}
