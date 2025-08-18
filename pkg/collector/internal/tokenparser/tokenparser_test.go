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
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestFromString(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue string
		wantToken string
	}{
		{
			name:      "invalid",
			input:     "x",
			wantValue: "x",
		},
		{
			name:      "success",
			input:     define.KeyToken + ":" + "token:value",
			wantValue: "value",
			wantToken: "token",
		},
	}

	for _, tt := range tests {
		value, token := FromString(tt.input)
		assert.Equal(t, tt.wantValue, value)
		assert.Equal(t, tt.wantToken, token)
	}
}

func makeHttpRequest(params, headers map[string]string, basicUser, basicPassword string) *http.Request {
	req := &http.Request{
		Header: make(http.Header),
		URL:    &url.URL{},
	}

	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if basicUser != "" || basicPassword != "" {
		req.SetBasicAuth(basicUser, basicPassword)
	}
	return req
}

func TestFromHttpRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			name: "token from url query",
			req:  makeHttpRequest(map[string]string{define.KeyToken: "foo"}, nil, "", ""),
			want: "foo",
		},
		{
			name: "token from header",
			req:  makeHttpRequest(nil, map[string]string{define.KeyToken: "bar"}, "", ""),
			want: "bar",
		},
		{
			name: "tenant from url query",
			req:  makeHttpRequest(map[string]string{define.KeyTenantID: "foo"}, nil, "", ""),
			want: "foo",
		},
		{
			name: "tenant from header",
			req:  makeHttpRequest(nil, map[string]string{define.KeyTenantID: "bar"}, "", ""),
			want: "bar",
		},
		{
			name: "valid basic auth",
			req:  makeHttpRequest(nil, nil, basicAuthUsername, "token1"),
			want: "token1",
		},
		{
			name: "invalid basic auth username",
			req:  makeHttpRequest(nil, nil, "foobar", ""),
		},
		{
			name: "invalid basic auth password",
			req:  makeHttpRequest(nil, nil, basicAuthUsername, ""),
		},
		{
			name: "valid bearer token",
			req:  makeHttpRequest(nil, map[string]string{"Authorization": "Bearer " + "foo"}, "", ""),
			want: "foo",
		},
		{
			name: "invalid bearer token format",
			req:  makeHttpRequest(nil, map[string]string{"Authorization": "InvalidToken"}, "", ""),
		},
		{
			name: "no token found",
			req:  makeHttpRequest(nil, nil, "", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromHttpRequest(tt.req); got != tt.want {
				t.Errorf("FromHttpRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFromGrpcMetadata(t *testing.T) {
	tests := []struct {
		name string
		md   metadata.MD
		want string
	}{
		{
			name: "valid KeyToken",
			md:   metadata.Pairs(define.KeyToken, "token"),
			want: "token",
		},
		{
			name: "valid KeyTenantID",
			md:   metadata.Pairs(define.KeyTenantID, "tenant"),
			want: "tenant",
		},
		{
			name: "both keys exist, prefer KeyToken",
			md: metadata.New(map[string]string{
				define.KeyToken:    "token",
				define.KeyTenantID: "tenant",
			}),
			want: "token",
		},
		{
			name: "multiple values in KeyToken",
			md: metadata.MD{
				strings.ToLower(define.KeyToken): []string{"tokenA", "tokenB"},
			},
			want: "tokenA",
		},
		{
			name: "multiple values in KeyTenantID",
			md: metadata.MD{
				strings.ToLower(define.KeyTenantID): []string{"tenantA", "tenantB"},
			},
			want: "tenantA",
		},
		{
			name: "no keys present",
			md:   metadata.New(nil),
		},
		{
			name: "empty KeyToken value",
			md:   metadata.Pairs(define.KeyToken, ""),
		},
		{
			name: "empty KeyTenantID value",
			md:   metadata.Pairs(define.KeyTenantID, ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromGrpcMetadata(tt.md); got != tt.want {
				t.Errorf("FromGrpcMetadata() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitKv(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "single valid pair",
			input: "key=value",
			want:  map[string]string{"key": "value"},
		},
		{
			name:  "multiple valid pairs",
			input: "a=1,b=2,c=3",
			want:  map[string]string{"a": "1", "b": "2", "c": "3"},
		},
		{
			name:  "with whitespace",
			input: "  name = John  ,  age=30  ",
			want:  map[string]string{"name": "John", "age": "30"},
		},
		{
			name:  "mixed valid and invalid",
			input: "valid=1,=emptykey,emptyval=,invalid,invalid=format=here",
			want:  map[string]string{"valid": "1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitKv(tt.input))
		})
	}
}
