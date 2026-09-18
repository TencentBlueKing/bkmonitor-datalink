// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"syscall"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/twmb/franz-go/pkg/kerr"
	"linkd/internal/config"
	"linkd/internal/consume"
)

type redisFailure string

func (e redisFailure) Error() string { return string(e) }

func (redisFailure) RedisError() {}

func TestTaskFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		reason, code string
		number       float64
	}{
		{name: "mysql", err: &mysql.MySQLError{Number: 1045, Message: "password=private-secret payload=private-payload"}, reason: "mysql_error", code: "mysql_error_code", number: 1045},
		{name: "kafka", err: kerr.SaslAuthenticationFailed, reason: "kafka_error", code: "kafka_error_code", number: 58},
		{name: "redis", err: redisFailure("WRONGPASS private-secret private-payload"), reason: "redis_error"},
		{name: "network", err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, reason: "network_error", code: "system_error_code", number: float64(syscall.ECONNREFUSED)},
		{name: "deadline", err: context.DeadlineExceeded, reason: "deadline_exceeded"},
		{name: "unknown", err: errors.New("private-secret private-payload"), reason: "task_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			a := Agent{Logger: slog.New(slog.NewJSONHandler(&out, nil)), RunTask: func(context.Context, Task, config.EventSource) error {
				return WithTaskStage("enrich_datasources", fmt.Errorf("connect: %w", tc.err))
			}}
			task := Task{ID: "source:lifecycle:0", Source: "source", Role: "lifecycle", Epoch: 12, Version: 3}
			err := a.runTask(t.Context(), task, config.EventSource{})
			if !errors.Is(err, tc.err) {
				t.Fatal("error chain lost")
			}
			if bytes.Contains(out.Bytes(), []byte("private-")) {
				t.Fatal("diagnostics leaked credentials or payload")
			}
			var record map[string]any
			if err := json.Unmarshal(out.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["event_source_id"] != "source" || record["role"] != "lifecycle" || record["assignment_epoch"] != float64(12) || record["stage"] != "enrich_datasources" || record["reason_code"] != tc.reason {
				t.Fatalf("diagnostic context=%v", record)
			}
			if tc.code != "" && record[tc.code] != tc.number {
				t.Fatalf("error code missing: %v", record)
			}
		})
	}
}

func TestTaskFailureCancellationAndUnexpectedExit(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := Agent{Logger: slog.New(slog.NewJSONHandler(&out, nil)), RunTask: func(context.Context, Task, config.EventSource) error { return context.Canceled }}
	if err := a.runTask(ctx, Task{}, config.EventSource{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("normal cancellation logged as failure")
	}
	a.RunTask = func(context.Context, Task, config.EventSource) error {
		return errors.Join(context.Canceled, &mysql.MySQLError{Number: 2013, Message: "private cleanup data"})
	}
	if err := a.runTask(ctx, Task{}, config.EventSource{}); err == nil || !bytes.Contains(out.Bytes(), []byte("mysql_error")) {
		t.Fatal("cancellation hid cleanup failure")
	}
	out.Reset()
	a.RunTask = func(context.Context, Task, config.EventSource) error {
		return errors.Join(context.Canceled, consume.ErrStopIncomplete)
	}
	if err := a.runTask(ctx, Task{}, config.EventSource{}); !errors.Is(err, consume.ErrStopIncomplete) || out.Len() == 0 {
		t.Fatal("incomplete shutdown hidden")
	}
	out.Reset()
	a.RunTask = func(context.Context, Task, config.EventSource) error { return nil }
	if err := a.runTask(t.Context(), Task{}, config.EventSource{}); err == nil || !bytes.Contains(out.Bytes(), []byte("task_return")) {
		t.Fatal("unexpected exit not diagnosed")
	}
	out.Reset()
	if err := a.runTask(ctx, Task{}, config.EventSource{}); err != nil || out.Len() != 0 {
		t.Fatal("successful shutdown logged")
	}
}
