// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"net/http"
	"testing"
	"time"
)

func TestDefaultPhaseTwoProductionHTTPClientUsesBoundedConnectionPool(t *testing.T) {
	dependencies := defaultPhaseTwoProductionExternalDependencies()
	client := dependencies.HTTPClient
	if client == nil {
		t.Fatal("default production dependencies have no HTTP client")
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want none because the per-attempt context deadline bounds requests", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want an explicit *http.Transport", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("transport has no proxy resolver, want http.ProxyFromEnvironment")
	}
	if transport.DialContext == nil {
		t.Fatal("transport has no bounded dialer")
	}
	dialer := phaseTwoUQDialer()
	if dialer.Timeout != 5*time.Second || dialer.KeepAlive != 30*time.Second {
		t.Fatalf("dialer timeout/keep-alive = %s/%s, want 5s/30s", dialer.Timeout, dialer.KeepAlive)
	}
	if transport.MaxIdleConns != 128 || transport.MaxIdleConnsPerHost != 64 {
		t.Fatalf("idle connections total/per host = %d/%d, want 128/64", transport.MaxIdleConns, transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("idle connection timeout = %s, want 90s", transport.IdleConnTimeout)
	}
	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLS handshake timeout = %s, want 10s", transport.TLSHandshakeTimeout)
	}
	if transport.ExpectContinueTimeout != time.Second {
		t.Fatalf("expect-continue timeout = %s, want 1s", transport.ExpectContinueTimeout)
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("response header timeout = %s, want none because the attempt deadline governs", transport.ResponseHeaderTimeout)
	}
}
