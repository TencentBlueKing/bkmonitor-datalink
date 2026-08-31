// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"strconv"
	"sync/atomic"
)

type statusScopeContextKey struct{}

var selectorStatusScopeSequence atomic.Uint64

func statusKey(ctx context.Context) string {
	if scope, ok := ctx.Value(statusScopeContextKey{}).(string); ok {
		return StatusKey + ":" + scope
	}
	return StatusKey
}

func withStatusScope(ctx context.Context, scope string) context.Context {
	base := GetStatus(ctx)
	scoped := context.WithValue(ctx, statusScopeContextKey{}, scope)
	if base != nil {
		copyOfBase := *base
		md.set(scoped, statusKey(scoped), &copyOfBase)
	}
	return scoped
}

// WithStatusScope creates an output-local status namespace while preserving
// the routing status that was recorded before output execution.
func WithStatusScope(ctx context.Context, outputIndex int) context.Context {
	return withStatusScope(ctx, "output:"+strconv.Itoa(outputIndex))
}

// WithSelectorStatusScope creates an internal selector-local status namespace.
// The scope inherits the output status at creation but writes cannot race with
// another selector populating under the same output context.
func WithSelectorStatusScope(ctx context.Context) context.Context {
	sequence := selectorStatusScopeSequence.Add(1)
	return withStatusScope(ctx, "selector:"+strconv.FormatUint(sequence, 10))
}

// Status
type Status struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SetStatus
func SetStatus(ctx context.Context, code, message string) {
	if code != "" {
		status := &Status{
			Code:    code,
			Message: message,
		}
		md.set(ctx, statusKey(ctx), status)
	}
}

// GetStatus
func GetStatus(ctx context.Context) *Status {
	r, ok := md.get(ctx, statusKey(ctx))
	if ok {
		if v, ok := r.(*Status); ok {
			return v
		}
	}
	return nil
}
