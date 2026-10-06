// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
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

// A repository that is never told its container still caches, at the product
// reference container's share. That fallback is a second copy of the same
// number in another package, so it is pinned here rather than left to a
// comment: this test is the only thing keeping the two from drifting.
func TestControlTimelineCacheDefaultMatchesReferenceContainer(t *testing.T) {
	repository, err := controlplane.NewRedisCatalogRepository(
		&unusedCatalogRedis{}, "alarmd:control:default-budget", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	occupancy := repository.ControlReadCacheStats().TimelineOccupancy
	reference := config.DeriveControlTimelineCache(config.ReferenceContainer())
	if occupancy.MaxBytes != reference.MaxBytes || occupancy.MaxEntries != reference.MaxEntries {
		t.Fatalf("unconfigured repository budget = %d bytes / %d entries, reference container derives %d / %d",
			occupancy.MaxBytes, occupancy.MaxEntries, reference.MaxBytes, reference.MaxEntries)
	}
}

// unusedCatalogRedis satisfies the constructor without a server: the budget is
// installed before any command is issued.
type unusedCatalogRedis struct{ redis.Cmdable }
