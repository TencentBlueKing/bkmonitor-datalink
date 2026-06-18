// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package grpcmiddleware

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/throttle"
)

func init() {
	Register("throttle", Throttle)
}

// Throttle 把限流注册为 grpc.InTapHandle，在读消息体之前按全方法名裁决。
func Throttle(_ string) grpc.ServerOption {
	// InTapHandle 是 gRPC 最省 CPU 的拒绝点，unary 与 streaming 都在首帧拦下。
	// 每个 server 只允许一个 tap，重复注册会 panic；它和既有 maxbytes（限消息大小）各管各的。
	return grpc.InTapHandle(func(ctx context.Context, info *tap.Info) (context.Context, error) {
		// 未注册的方法不限流。
		recordType := throttle.ClassifyGRPC(info.FullMethodName)
		if recordType == define.RecordUndefined {
			return ctx, nil
		}

		action := throttle.GlobalManager().Decide(recordType)
		if action == throttle.ActionAdmit {
			return ctx, nil
		}

		// 反序列化之前就拒，省掉为废请求白付的解码开销。
		throttle.IncDropped(define.RequestGrpc, recordType, action)
		return nil, status.Error(codes.ResourceExhausted, "collector overloaded")
	})
}
