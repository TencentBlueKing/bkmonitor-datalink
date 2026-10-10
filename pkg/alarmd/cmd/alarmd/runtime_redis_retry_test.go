// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

// The runtime client takes one attempt per call, because the layer above it
// already retries and can do something the client cannot.
//
// go-redis defaults to three retries. Stacked under a scheduler that records a
// failed attempt, parks the Query Group with exponential backoff and no attempt
// cap, and re-reads current state on the way back, they add no reliability -
// the same bytes are resent to the same server - and they multiply the cost: a
// 3 s read timeout becomes 12 s of a worker held on a read that fails
// identically every time.
//
// On a short-period Plan they do worse than cost time. That Plan's completion
// context carries a hard deadline, so 12 s spent inside it means the Slot dies
// with the context already expired, and an expired context is the first thing
// executionErrorBacksOff refuses - no attempt recorded, no retry scheduled. The
// two retry layers together turned a retryable failure into one nothing
// retries, which is the opposite of what the inner layer was there for.
func TestTheRuntimeRedisClientTakesOneAttemptPerCall(t *testing.T) {
	connection := config.RedisConnectionConfig{
		Address: "127.0.0.1:6379", ReadTimeout: config.Duration(3 * time.Second),
		WriteTimeout: config.Duration(3 * time.Second), DialTimeout: config.Duration(3 * time.Second),
	}
	options := productionRedisOptions(connection)
	if options.MaxRetries != -1 {
		t.Fatalf("MaxRetries = %d, want -1 (one attempt); at the go-redis default of 3 a %s read timeout "+
			"holds a worker for %s and can outlive a short-period Plan's completion deadline, "+
			"after which nothing retries the Slot at all",
			options.MaxRetries, connection.ReadTimeout.Duration(), 4*connection.ReadTimeout.Duration())
	}
	// The read timeout is still the client's own bound, so the failure arrives
	// as a timeout to classify rather than as a call that never returns.
	if options.ReadTimeout != connection.ReadTimeout.Duration() {
		t.Fatalf("ReadTimeout = %s, want the configured %s", options.ReadTimeout, connection.ReadTimeout.Duration())
	}
}
