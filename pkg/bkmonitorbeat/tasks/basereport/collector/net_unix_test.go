// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"testing"
)

func Test_ProtoCountersUnix(t *testing.T) {
	var protocols = []string{"udp", "tcp", "ip"}
	data, err := ProtoCounters(protocols)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 3 {
		t.Fatal("not get all protocol (udp tcp ip)")
	}
	for _, v := range data {
		if v.Protocol != "udp" && v.Protocol != "tcp" && v.Protocol != "ip" {
			t.Fatal("get wrong protocol")
		}
		if len(v.Stats) == 0 {
			t.Fatal("protocol get wrong data")
		}
	}
	t.Log(data)
}
