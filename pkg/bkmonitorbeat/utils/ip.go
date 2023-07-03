// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"net"
	"strings"
)

type DomainType int32

const (
	Domain DomainType = 0
	V4     DomainType = 4
	V6     DomainType = 6
)

// 判断addr是否为有效的ipv4 ipv6 或者域名
// 返回值： 0-有效域名  4-有效ipv4  6-有效ipv6
func CheckIpOrDomainValid(addr string) DomainType {
	ip := net.ParseIP(strings.Split(addr, ":")[0])
	if ip.To4() != nil {
		return V4
	}
	ip = net.ParseIP(addr)
	if ip.To16() != nil || strings.Contains(addr, "]:") == true {
		return V6
	}
	return Domain
}

// 通过ipType筛选ip列表，提取指定类型的ip
func FilterIpsWithIpType(ips []string, ipType DomainType) []string {
	var s []string
	for _, v := range ips {
		if CheckIpOrDomainValid(v) == ipType {
			s = append(s, v)
		}
	}
	return s
}
