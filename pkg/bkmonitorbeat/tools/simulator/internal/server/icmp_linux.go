// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package server

import (
	"os"
)

const (
	ipv4ICMPFile = "/proc/sys/net/ipv4/icmp_echo_ignore_all"
	ipv6ICMPFile = "/proc/sys/net/ipv6/icmp/echo_ignore_all"
)

func setICMP(on bool) error {
	var v string
	if on {
		v = "0"
	} else {
		v = "1"
	}
	err := os.WriteFile(ipv4ICMPFile, []byte(v), 0644)
	if err != nil {
		return err
	}
	err = os.WriteFile(ipv6ICMPFile, []byte(v), 0644)
	return err
}
