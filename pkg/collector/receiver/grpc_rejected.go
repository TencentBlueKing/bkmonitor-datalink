// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/grpcmiddleware"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// newGrpcServer builds the receiving gRPC server: the configured middlewares,
// and a stats handler that counts the requests gRPC itself refuses.
func newGrpcServer(middlewares []string) *grpc.Server {
	opts := []grpc.ServerOption{grpc.StatsHandler(rejectedStats{})}
	for _, mid := range middlewares {
		opt := grpcmiddleware.Get(mid)
		if opt != nil {
			logger.Debugf("receiver/grpc use '%s' middleware", mid)
			opts = append(opts, opt)
		}
	}
	return grpc.NewServer(opts...)
}

// rejectedStats counts the requests gRPC refuses before or while decoding
// the message, which no service handler sees: a message over the receive
// size limit, one that does not decompress, and one that does not decode.
// gRPC reports them only as the status the call ends with, so they are told
// apart by the code and the message gRPC writes for each.
type rejectedStats struct{}

func (rejectedStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

func (rejectedStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (rejectedStats) HandleConn(context.Context, stats.ConnStats) {}

func (rejectedStats) HandleRPC(_ context.Context, s stats.RPCStats) {
	end, ok := s.(*stats.End)
	if !ok || end.Error == nil {
		return
	}
	if reason, ok := grpcRejectReason(status.Convert(end.Error)); ok {
		define.IncReceiverRejected(define.RequestGrpc, reason)
	}
}

func grpcRejectReason(st *status.Status) (define.RejectReason, bool) {
	msg := st.Message()
	switch st.Code() {
	case codes.ResourceExhausted:
		// Over the receive limit, before or after decompression. The throttle
		// answers ResourceExhausted too, with its own message.
		if strings.HasPrefix(msg, "grpc: received message larger than max") ||
			strings.HasPrefix(msg, "grpc: received message after decompression larger than max") {
			return define.RejectTooLarge, true
		}
	case codes.Internal:
		if strings.HasPrefix(msg, "grpc: error unmarshalling request") {
			return define.RejectDecode, true
		}
		if strings.HasPrefix(msg, "grpc: failed to decompress the received message") {
			return define.RejectDecompress, true
		}
	}
	return "", false
}
