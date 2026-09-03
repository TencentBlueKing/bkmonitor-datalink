// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux

package timesync

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestQueryChronyTimeoutClosesConn(t *testing.T) {
	originalNewChronyConn := newChronyConn
	defer func() {
		newChronyConn = originalNewChronyConn
	}()

	clientConn, serverConn := net.Pipe()
	newChronyConn = func(addr string, timeout time.Duration) (net.Conn, error) {
		return clientConn, nil
	}

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer serverConn.Close()

		buf := make([]byte, 1024)
		_, _ = serverConn.Read(buf)
		time.Sleep(200 * time.Millisecond)
	}()

	cli := NewClient(&Option{
		ChronyAddr: "[::1]:323",
		Timeout:    50 * time.Millisecond,
	})

	start := time.Now()
	_, err := cli.queryChrony()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("expected timeout-related error, got %v", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected queryChrony to return near timeout, took %v", elapsed)
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server side connection was not closed in time")
	}
}
