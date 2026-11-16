// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"net"
	"strings"
)

// 按配置没有创建 Consul client 的具名原因。
const (
	ReasonAddressNotConfigured = "consul_address_not_configured"
	ReasonAddressMissingHost   = "consul_address_missing_host"
)

// AddressError 说明地址为何没有创建 client；此后所有 Consul 调用都返回它，不再连接任何地址。
type AddressError struct {
	Reason string
}

func (e *AddressError) Error() string {
	return "consul client is not created: " + e.Reason
}

// checkAddress 按 consul api 的拆分方式检查地址：空地址视为未配置，不能交给 api 补成本机默认地址；
// 不带 scheme 或 http、https 的地址必须带 host。unix socket 没有 host，其他 scheme 由 api 自己报错。
func checkAddress(address string) *AddressError {
	if strings.TrimSpace(address) == "" {
		return &AddressError{Reason: ReasonAddressNotConfigured}
	}
	scheme, rest := "", address
	if parts := strings.SplitN(address, "://", 2); len(parts) == 2 {
		scheme, rest = parts[0], parts[1]
	}
	if scheme != "" && scheme != "http" && scheme != "https" {
		return nil
	}
	host := strings.SplitN(rest, "/", 2)[0]
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.TrimSpace(host) == "" {
		return &AddressError{Reason: ReasonAddressMissingHost}
	}
	return nil
}
