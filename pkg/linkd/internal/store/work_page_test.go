// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"context"
	"errors"
	"testing"
)

func TestWorkPageShrinksWithoutSkippingLargeRecords(t *testing.T) {
	var limits []int
	value, n, err := ReadWorkPage(t.Context(), 64, func(_ context.Context, n int) (string, error) {
		limits = append(limits, n)
		if n > 8 {
			return "", ErrPageTooLarge
		}
		return "same cursor", nil
	})
	if err != nil || value != "same cursor" || n != 8 || len(limits) != 4 {
		t.Fatal(value, n, err, limits)
	}
	calls := 0
	_, n, err = ReadWorkPage(t.Context(), 2, func(context.Context, int) (string, error) { calls++; return "", ErrPageTooLarge })
	if !errors.Is(err, ErrPageTooLarge) || n != 1 || calls != 2 {
		t.Fatal(n, err, calls)
	}
}
