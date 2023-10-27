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

	"github.com/elastic/beats/libbeat/common"
)

var config *common.Config

func Test_Send_NewGseDynamicMsg(t *testing.T) {
	cli, err := NewGseClient(config)
	if err != nil {
		t.Fatal(err)
	}

	err = cli.Start()
	if err != nil {
		t.Fatal(err)
	}

	m := NewGseDynamicMsg([]byte("test hc"), 1430, 0, 0)
	m.AddMeta("tlogc", "2017-01-09 21:18")
	cli.Send(m)
	time.Sleep(3 * time.Second)
	cli.Close()
}

func newOpMsg() GseMsg {
	date := time.Now().String()
	m := NewGseOpMsg([]byte(date), 1430, 0, 0, 0)
	return m
}

func Test_SendWithNewConnection(t *testing.T) {
	cli, err := NewGseClient(config)
	if err != nil {
		t.Fatal(err)
	}
	err = cli.Start()
	if err != nil {
		t.Fatal(err)
	}

	t.Log("send one op data")
	cli.SendWithNewConnection(newOpMsg())
	time.Sleep(1 * time.Second)
	t.Log("send one op data")
	cli.SendWithNewConnection(newOpMsg())
	time.Sleep(1 * time.Second)
	t.Log("send one op data")
	cli.SendWithNewConnection(newOpMsg())
	time.Sleep(1 * time.Second)
	cli.Close()
}
