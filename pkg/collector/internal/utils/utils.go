// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"context"
	"math"
	"net"

	"google.golang.org/grpc/peer"
)

func ParseRequestIP(source string) string {
	s, _, err := net.SplitHostPort(source)
	if err == nil {
		return s
	}
	ip := net.ParseIP(source)
	if ip != nil {
		return ip.String()
	}
	return ""
}

func GetGrpcIpFromContext(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return ParseRequestIP(p.Addr.String())
	}
	return ""
}

func CloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	ret := make(map[string]string)
	for key, value := range m {
		ret[key] = value
	}
	return ret
}

func MergeMap(ms ...map[string]string) map[string]string {
	ret := make(map[string]string)
	for _, m := range ms {
		for k, v := range m {
			ret[k] = v
		}
	}

	return ret
}

func IsValidFloat64(f float64) bool {
	return !(math.IsNaN(f) || math.IsInf(f, 0))
}

func IsValidUint64(u uint64) bool {
	return !(u == uint64(math.NaN()) || u == uint64(math.Inf(0)))
}
