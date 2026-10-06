// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"
	"time"
)

// PreflightTiming splits a state preflight's time into what the store's
// reads took, round trip included, and what reading the returned bytes into
// views took in this process. The phase's total could not say which of the
// two a slow round spent its time on: a Query Group whose records decode to
// fifty times their stored size looks exactly like a slow store from outside.
//
// It travels on the context rather than on StatePreflightResult because it
// is a measurement of this call, not part of what was read: two reads of the
// same state are the same result and never the same durations.
type PreflightTiming struct {
	Fetch  time.Duration
	Decode time.Duration
}

type preflightTimingKey struct{}

// WithPreflightTiming returns a context the store adds its preflight timing
// to, and the timing it adds to.
func WithPreflightTiming(ctx context.Context) (context.Context, *PreflightTiming) {
	timing := &PreflightTiming{}
	return context.WithValue(ctx, preflightTimingKey{}, timing), timing
}

// PreflightTimingFrom is the timing a caller asked for, or nil.
func PreflightTimingFrom(ctx context.Context) *PreflightTiming {
	timing, _ := ctx.Value(preflightTimingKey{}).(*PreflightTiming)
	return timing
}
