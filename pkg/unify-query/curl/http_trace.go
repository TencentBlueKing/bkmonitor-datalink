// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").
// You may obtain a copy of the License at
// http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.

package curl

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http/httptrace"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	connectionFingerprintSalt  [32]byte
	connectionFingerprintReady = func() bool {
		_, err := rand.Read(connectionFingerprintSalt[:])
		return err == nil
	}()
)

func withHTTPClientTrace(ctx context.Context, span *trace.Span) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			span.Set("outbound.connection.reused", info.Reused)
			span.Set("outbound.connection.was_idle", info.WasIdle)
			span.Set("outbound.connection.idle_time_ns", info.IdleTime.Nanoseconds())
			if fingerprint := connectionFingerprint(info.Conn); fingerprint != "" {
				span.Set("outbound.connection.fingerprint", fingerprint)
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			span.Set("outbound.request.write_complete", info.Err == nil)
			if info.Err != nil {
				span.Set("outbound.request.write_error", info.Err.Error())
			}
		},
	})
}

func connectionFingerprint(conn net.Conn) string {
	if conn == nil || !connectionFingerprintReady {
		return ""
	}
	local, remote := conn.LocalAddr(), conn.RemoteAddr()
	if local == nil || remote == nil {
		return ""
	}

	addressPair := strings.Join([]string{
		local.Network(), local.String(), remote.Network(), remote.String(),
	}, "\x00")
	fingerprint := hmac.New(sha256.New, connectionFingerprintSalt[:])
	_, _ = fingerprint.Write([]byte(addressPair))
	return hex.EncodeToString(fingerprint.Sum(nil)[:16])
}
