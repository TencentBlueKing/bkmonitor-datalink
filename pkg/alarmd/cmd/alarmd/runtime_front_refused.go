// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// frontRefusedEvery is how often one refusal code for one operation writes
// its line; frontRefusedKeys bounds the pairs remembered.
const (
	frontRefusedEvery = 10 * time.Minute
	frontRefusedKeys  = 1024
	// frontOperationBytes bounds the operation name written: it is the
	// caller's, not a name this build defines.
	frontOperationBytes = 64
)

// frontRefusedLine writes a refused front read with its code and the
// operation it asked for, at most once per pair a period, with the lines
// held back counted on the next. A page calling an operation this build no
// longer serves it read blank, and alarmd said nothing.
func frontRefusedLine(logger *observability.Logger, now func() time.Time) func(code, operation string) {
	if logger == nil {
		return nil
	}
	var mu sync.Mutex
	last := map[string]time.Time{}
	held := map[string]int{}
	return func(code, operation string) {
		operation = boundedOperation(operation)
		at := now()
		key := code + "\x00" + operation
		mu.Lock()
		if written, ok := last[key]; ok && at.Sub(written) < frontRefusedEvery {
			held[key]++
			mu.Unlock()
			return
		}
		if len(last) >= frontRefusedKeys {
			for k, written := range last {
				if at.Sub(written) >= 2*frontRefusedEvery {
					delete(last, k)
					delete(held, k)
				}
			}
		}
		suppressed := held[key]
		last[key] = at
		delete(held, key)
		mu.Unlock()
		logger.WarnEvent("front_read_refused", code, 0, slog.String("code", code), slog.String("operation", operation),
			slog.Int("held_since_last", suppressed))
	}
}

// boundedOperation is the operation name as written, cut to
// frontOperationBytes and to the characters an operation name has.
func boundedOperation(operation string) string {
	out := make([]byte, 0, min(len(operation), frontOperationBytes))
	for i := 0; i < len(operation) && len(out) < frontOperationBytes; i++ {
		c := operation[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			out = append(out, c)
		} else {
			out = append(out, '?')
		}
	}
	return string(out)
}
