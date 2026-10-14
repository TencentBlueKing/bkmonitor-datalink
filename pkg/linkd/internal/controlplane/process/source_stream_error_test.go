// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	streams "linkd/internal/controlplane/redisstream"
)

type streamRedisError string

func (e streamRedisError) Error() string { return string(e) }

func (streamRedisError) RedisError() {}

func TestSourceStreamLogsOperationAndClassWithoutPrivateError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{context.DeadlineExceeded, "stream_timeout"}, {context.Canceled, "stream_canceled"},
		{&net.DNSError{Err: "private-network-host", IsTimeout: true}, "stream_timeout"},
		{&net.DNSError{Err: "private-network-host"}, "stream_connection_failed"},
		{io.EOF, "stream_connection_failed"},
		{streamRedisError("NOPERM private-password"), "stream_permission_denied"},
		{streamRedisError("WRONGTYPE private-key"), "stream_wrong_type"},
		{streamRedisError("READONLY private-node"), "stream_unavailable"},
		{errors.New("private-server-response"), "stream_reconcile_failed"},
	} {
		t.Run(tc.code+fmt.Sprintf("%T", tc.err), func(t *testing.T) {
			var buffer bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buffer, nil))
			err := fmt.Errorf("outer: %w", &streams.OperationError{Operation: "XTRIM", Err: tc.err})
			logSourceStreamFailure(t.Context(), logger, "source-a", 3*time.Second, err)
			var record map[string]any
			if e := json.Unmarshal(buffer.Bytes(), &record); e != nil {
				t.Fatal(e)
			}
			if record["error_code"] != tc.code || record["operation"] != "XTRIM" || record["event_source_id"] != "source-a" || record["duration_ms"] != float64(3000) {
				t.Fatal(record)
			}
			if strings.Contains(buffer.String(), "private-") {
				t.Fatal("private error escaped")
			}
		})
	}
	var buffer bytes.Buffer
	logSourceStreamFailure(t.Context(), slog.New(slog.NewJSONHandler(&buffer, nil)), "source-a", 0, &streams.OperationError{Operation: "private-command", Err: errors.New("private-error")})
	if strings.Contains(buffer.String(), "private-") {
		t.Fatal("operation must be allowlisted")
	}
}
