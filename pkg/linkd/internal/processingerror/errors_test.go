// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processingerror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestRetryClassificationAndPanicIsolation(t *testing.T) {
	cases := []struct {
		err   error
		retry bool
	}{
		{HTTPStatus(502), false}, {HTTPStatus(429), false}, {HTTPStatus(400), false},
		{fmt.Errorf("wrapped: %w", RetryableError{HTTPStatus(503)}), true},
		{errors.New("HTTP 502 in arbitrary error text"), false},
		{&json.SyntaxError{}, false}, {context.Canceled, false},
		{errors.Join(HTTPStatus(502), errors.New("invalid JSON")), false},
		{errors.Join(RetryableError{HTTPStatus(502)}, RetryableError{HTTPStatus(503)}), true},
	}
	for _, tc := range cases {
		if got := Retryable(tc.err); got != tc.retry {
			t.Fatalf("%T retry %v", tc.err, got)
		}
	}
	_, err := Guard(func() (int, error) { panic("private request body") })
	if !errors.Is(err, ErrPanic) || Retryable(err) {
		t.Fatal("panic not safely classified", err)
	}
}
