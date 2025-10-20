// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenparser

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TarsCloud/TarsGo/tars/util/current"
	"google.golang.org/grpc/metadata"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

const (
	basicAuthUsername = "bkmonitor"
)

func WrapProxyToken(token define.Token) string {
	return fmt.Sprintf("%d/%s", token.ProxyDataId, token.Original)
}

func FromHttpRequest(req *http.Request) string {
	// 1) 从 tokenKey 中读取
	token := req.URL.Query().Get(define.KeyToken)
	if token == "" {
		token = req.Header.Get(define.KeyToken)
	}
	if token != "" {
		return token
	}

	// 2) 从 tenantidKey 中读取
	token = req.Header.Get(define.KeyTenantID)
	if token == "" {
		token = req.URL.Query().Get(define.KeyTenantID)
	}
	if token != "" {
		return token
	}

	// 3）从 basicauth 中读取（当且仅当 username 为 bkmonitor 才生效
	username, password, ok := req.BasicAuth()
	if ok && username == basicAuthUsername && password != "" {
		return password
	}

	// 4）从 bearerauth 中读取 token
	bearer := strings.Split(req.Header.Get("Authorization"), "Bearer ")
	if len(bearer) == 2 {
		return bearer[1]
	}

	// 弃疗 ┓(-´∀`-)┏
	return ""
}

func FromGrpcMetadata(md metadata.MD) string {
	// 1) 从 tokenKey 中读取
	token := md.Get(define.KeyToken)
	if len(token) > 0 {
		return token[0]
	}

	// 2) 从 tenantidKey 中读取
	token = md.Get(define.KeyTenantID)
	if len(token) > 0 {
		return token[0]
	}
	return ""
}

// FromTarsCtx 从 Tars ctx（类似 gPRC MetaData）中提取 token
func FromTarsCtx(ctx context.Context) string {
	rc, ok := current.GetRequestContext(ctx)
	if !ok {
		return ""
	}
	token, ok := rc[define.KeyToken]
	if !ok {
		return ""
	}
	return token
}

// FromString 从 {KeyToken}:{token}:value 中提取 token
func FromString(s string) (string, string) {
	if !strings.HasPrefix(s, define.KeyToken) {
		return s, ""
	}
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return s, ""
	}
	return parts[2], parts[1]
}

func FromHttpUserMetadata(req *http.Request) map[string]string {
	meta := req.Header.Get(define.KeyUserMetadata)
	return splitKv(meta)
}

func FromGrpcUserMetadata(md metadata.MD) map[string]string {
	meta := md.Get(define.KeyUserMetadata)
	if len(meta) == 0 {
		return nil
	}
	return splitKv(meta[0])
}

func splitKv(s string) map[string]string {
	if s == "" {
		return nil
	}

	ret := make(map[string]string)
	kvs := strings.Split(s, ",")
	for i := 0; i < len(kvs); i++ {
		kv := strings.Split(kvs[i], "=")
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key == "" || val == "" {
			continue
		}
		ret[key] = val
	}
	return ret
}
