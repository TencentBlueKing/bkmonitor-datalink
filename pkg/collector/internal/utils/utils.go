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
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/TarsCloud/TarsGo/tars/util/current"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/grpc/peer"
)

func ParseRequestIP(source string, header http.Header) string {
	if header != nil {
		forwarded := header.Get("X-Forwarded-For")
		if forwarded != "" {
			return forwarded
		}
	}

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

func GetContentLength(header http.Header) int {
	l := header.Get("Content-Length")
	i, _ := strconv.Atoi(l)
	return i
}

func GetGrpcIpFromContext(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return ParseRequestIP(p.Addr.String(), nil)
	}
	return ""
}

func GetTarsIpFromContext(ctx context.Context) string {
	if ip, ok := current.GetClientIPFromContext(ctx); ok {
		return ip
	}
	return ""
}

func IsValidFloat64(f float64) bool {
	return !(math.IsNaN(f) || math.IsInf(f, 0))
}

func IsValidUint64(u uint64) bool {
	return !(u == uint64(math.NaN()) || u == uint64(math.Inf(0)))
}

// FirstUpper 仅首字母大写，过滤掉驼峰的情况
func FirstUpper(s, defaultVal string) string {
	if s == "" {
		return defaultVal
	}
	s = strings.ToLower(s)
	return strings.ToUpper(s[:1]) + s[1:]
}

func CalcSpanDuration(span ptrace.Span) float64 {
	if span.StartTimestamp() > span.EndTimestamp() {
		return 0 // 特殊处理 避免出现超大值
	}
	return float64(span.EndTimestamp() - span.StartTimestamp())
}

func PathExist(path string) bool {
	_, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}
