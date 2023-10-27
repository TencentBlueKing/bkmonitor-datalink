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
	"strings"

	"github.com/cenkalti/backoff"
)

// ContextExponentialRetry
func ContextExponentialRetry(ctx context.Context, fn func() error) error {
	bf := backoff.NewExponentialBackOff()
	return backoff.Retry(fn, backoff.WithContext(bf, ctx))
}

// ExponentialRetry
func ExponentialRetry(n int, fn func() error) error {
	bf := backoff.NewExponentialBackOff()
	return backoff.Retry(fn, backoff.WithMaxRetries(bf, uint64(n)))
}

// Partition : Search for the separator sep in S, and return the part before it, the separator itself, and the part after it.
// If the separator is not found, return S and two empty strings.
func Partition(s, sep string) (string, string, string) {
	parts := strings.SplitN(s, sep, 2)
	if len(parts) != 2 {
		return s, "", ""
	}
	return parts[0], sep, parts[1]
}
