// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package gse

import (
	"testing"
	"time"
)

func Test_GseSimpleClient(t *testing.T) {
	cli := NewGseSimpleClient()
	cli.SetAgentHost(MockAddress)
	err := cli.Start()
	if err != nil {
		t.Fatal(err)
	}

	info := AgentInfo{}
	// get agent info
	go func() {
		info, err = cli.SyncGetAgentInfo()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// timewait to get agent info
	time.Sleep(1 * time.Second)

	if info.IsEmpty() {
		t.Fatal("request agent info timeout")
	}

	if info.Bizid != 1 {
		t.Fatal("get companyid error")
	}
	if info.Cloudid != 2 {
		t.Fatal("get plat_id error")
	}
	if info.IP != "127.0.0.1" {
		t.Fatal("get ip error")
	}

	cli.Close()

	// send msg
	md := NewGseDynamicMsg([]byte("test dynamic"), 1430, 0, 0)
	md.AddMeta("tlogc", "2017-01-09 21:18")
	cli.Send(md)

	// send msg
	mc := NewGseCommonMsg([]byte("test common"), 1430, 0, 0, 0)
	cli.Send(mc)
}
