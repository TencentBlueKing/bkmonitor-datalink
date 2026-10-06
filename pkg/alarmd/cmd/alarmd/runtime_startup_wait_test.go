// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// serverReply is a reply the Redis server sent, as go-redis types it.
type serverReply string

func (reply serverReply) Error() string { return string(reply) }
func (serverReply) RedisError()         {}

// fenceRefused is how the fence probe reports a reply, wrapped.
func fenceRefused(reply error) error {
	return fmt.Errorf("alarmd ownership: the owner fence cannot run on this Redis: %w", reply)
}

// Each reply that means "not now" is waited on, wrapped the way the fence
// probe wraps it or not; every other reply is a refusal and ends startup.
func TestStartupWaitsOnServerRepliesThatMeanNotNowAndEndsOnRefusals(t *testing.T) {
	waits := []string{
		"LOADING Redis is loading the dataset in memory",
		"MASTERDOWN Link with MASTER is down and replica-serve-stale-data is set to 'no'.",
		"TRYAGAIN Multiple keys request during rehashing of slot",
		"CLUSTERDOWN The cluster is down",
		"READONLY You can't write against a read only replica.",
		"BUSY Redis is busy running a script.",
	}
	for _, reply := range waits {
		for _, err := range []error{serverReply(reply), fenceRefused(serverReply(reply))} {
			if !startupDependencyTransient(err) {
				t.Errorf("%q was not waited on", err)
			}
		}
	}
	refusals := []string{
		"NOSCRIPT No matching script.",
		"ERR unknown command 'EVAL'",
		"ERR Error running script: user_script:1: attempt to call field 'replicate_commands'",
		"WRONGPASS invalid username-password pair or user is disabled.",
		"NOAUTH Authentication required.",
		// A prefix that only starts like a waited one is not that reply.
		"LOADINGX not a reply Redis sends",
	}
	for _, reply := range refusals {
		for _, err := range []error{serverReply(reply), fenceRefused(serverReply(reply))} {
			if startupDependencyTransient(err) {
				t.Errorf("%q was waited on, want startup to end", err)
			}
		}
	}
}

// A dependency that does not answer at all is waited on.
func TestStartupWaitsOnADependencyThatDoesNotAnswer(t *testing.T) {
	_, refused := net.Dial("tcp", "127.0.0.1:1")
	if refused == nil {
		t.Skip("something answers on port 1")
	}
	for _, err := range []error{
		refused, io.EOF, fmt.Errorf("read: %w", io.ErrUnexpectedEOF), context.DeadlineExceeded,
		errors.New("redis: all sentinels specified in configuration are unreachable"),
		errors.New("redis: connection pool timeout"),
	} {
		if !startupDependencyTransient(err) {
			t.Errorf("%v was not waited on", err)
		}
	}
	if startupDependencyTransient(errors.New("alarmd: a configuration the program cannot use")) {
		t.Error("an error that is neither a reply nor the network was waited on")
	}
}

// The wait retries until the dependency answers, counts each failed attempt
// under the dependency's name, holds the replica not ready under a reason,
// and returns the first refusal without counting it.
func TestTheStartupWaitRetriesCountsAndStopsOnAnAnswerOrARefusal(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	health := newPhaseTwoApplicationHealth()
	waiter := startupWaiter{initial: time.Millisecond, ceiling: 2 * time.Millisecond, recorder: recorder,
		logger: observability.Discard(observability.ComponentRuntime), health: health}
	attempts := 0
	err := waiter.await(context.Background(), "redis_runtime", func(context.Context) error {
		attempts++
		if attempts < 4 {
			if attempts == 3 {
				snapshot := health.HealthSnapshot()
				if snapshot.Ready || len(snapshot.Reasons) != 1 || snapshot.Reasons[0] != observability.ReasonCode(contract.ReasonRedisUnavailable) {
					t.Errorf("while waiting: %+v, want not ready under %s", snapshot, contract.ReasonRedisUnavailable)
				}
			}
			return serverReply("LOADING Redis is loading the dataset in memory")
		}
		return nil
	})
	if err != nil || attempts != 4 {
		t.Fatalf("err %v after %d attempts, want an answer on the fourth", err, attempts)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_startup_dependency_wait_total", map[string]string{"dependency": "redis_runtime"}); got != 3 {
		t.Fatalf("startup_dependency_wait_total{redis_runtime} = %v, want 3", got)
	}

	attempts = 0
	refusal := serverReply("WRONGPASS invalid username-password pair or user is disabled.")
	err = waiter.await(context.Background(), "state_store", func(context.Context) error { attempts++; return refusal })
	if !errors.Is(err, refusal) || attempts != 1 {
		t.Fatalf("err %v after %d attempts, want the refusal on the first", err, attempts)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_startup_dependency_wait_total", map[string]string{"dependency": "state_store"}); got != 0 {
		t.Fatalf("a refusal was counted as a wait: %v", got)
	}
}

// The delay doubles to its ceiling and no further.
func TestTheStartupWaitBacksOffToItsCeiling(t *testing.T) {
	waiter := startupWaiter{initial: 50 * time.Millisecond, ceiling: 100 * time.Millisecond}
	var at []time.Time
	_ = waiter.await(context.Background(), "redis_source", func(context.Context) error {
		at = append(at, time.Now())
		if len(at) < 6 {
			return io.EOF
		}
		return nil
	})
	// Without the ceiling the fourth and fifth gaps would be 200ms and 400ms,
	// past the half-again tolerance.
	want := []time.Duration{50, 100, 100, 100, 100}
	for i, floor := range want {
		gap := at[i+1].Sub(at[i])
		if gap < floor*time.Millisecond || gap > floor*time.Millisecond*3/2 {
			t.Fatalf("gap %d = %s, want about %dms (gaps from %v)", i, gap, floor, at)
		}
	}
}

// Stopping the process ends the wait within one backoff, with an error that
// names the dependency it was waiting on.
func TestTheStartupWaitEndsWithTheProcess(t *testing.T) {
	waiter := startupWaiter{initial: time.Hour, ceiling: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- waiter.await(ctx, "cmdb_index", func(context.Context) error { return io.EOF })
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want the cancellation and the last failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait did not end with its context")
	}
}
