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
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"time"

	redis "github.com/redis/go-redis/v9"
	streams "linkd/internal/controlplane/redisstream"
)

// sourceStreamErrorCode 只输出固定类别，原始 Redis / 网络错误可能含连接或认证信息。
func sourceStreamErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "stream_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "stream_canceled"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "stream_timeout"
		}
		return "stream_connection_failed"
	}
	if errors.Is(err, io.EOF) {
		return "stream_connection_failed"
	}
	for _, prefix := range []string{"NOAUTH", "WRONGPASS", "NOPERM"} {
		if redis.HasErrorPrefix(err, prefix) {
			return "stream_permission_denied"
		}
	}
	if redis.HasErrorPrefix(err, "WRONGTYPE") {
		return "stream_wrong_type"
	}
	for _, prefix := range []string{"LOADING", "CLUSTERDOWN", "READONLY", "BUSY"} {
		if redis.HasErrorPrefix(err, prefix) {
			return "stream_unavailable"
		}
	}
	return "stream_reconcile_failed"
}

func logSourceStreamFailure(ctx context.Context, logger *slog.Logger, source string, duration time.Duration, err error) {
	operation := "unknown"
	var failure *streams.OperationError
	if errors.As(err, &failure) {
		switch failure.Operation {
		case "EXISTS", "XINFO STREAM", "XINFO GROUPS", "MEMORY USAGE", "XPENDING", "XTRIM":
			operation = failure.Operation
		}
	}
	logger.WarnContext(ctx, "source stream reconcile failed", "event_source_id", source,
		"operation", operation, "error_code", sourceStreamErrorCode(err), "duration_ms", duration.Milliseconds())
}
