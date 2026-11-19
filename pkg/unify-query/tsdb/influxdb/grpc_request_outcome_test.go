// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"errors"
	"fmt"
	"testing"

	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCRequestOutcomeAlignsWithHTTP(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"no error":                   {nil, "success"},
		"unavailable":                {status.Error(codes.Unavailable, "down"), "error"},
		"internal":                   {status.Error(codes.Internal, "boom"), "error"},
		"resource exhausted (429)":   {status.Error(codes.ResourceExhausted, "throttled"), "error"},
		"unauthenticated (401)":      {status.Error(codes.Unauthenticated, "no"), "error"},
		"permission denied (403)":    {status.Error(codes.PermissionDenied, "no"), "error"},
		"invalid argument":           {status.Error(codes.InvalidArgument, "bad"), "rejected"},
		"out of range":               {status.Error(codes.OutOfRange, "bad"), "rejected"},
		"grpc deadline":              {status.Error(codes.DeadlineExceeded, "slow"), "timeout"},
		"grpc canceled":              {status.Error(codes.Canceled, "gone"), "canceled"},
		"wrapped grpc status":        {pkgerrors.Wrapf(status.Error(codes.Unavailable, "down"), "receive series from %s", "grpc://192.0.2.30"), "error"},
		"frame timeout":              {pkgerrors.Wrapf(context.DeadlineExceeded, "failed to receive any data"), "timeout"},
		"context canceled":           {fmt.Errorf("recv: %w", context.Canceled), "canceled"},
		"not a grpc status, untyped": {errors.New("decode frame"), "rejected"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.want, grpcRequestOutcome(c.err))
		})
	}
}
