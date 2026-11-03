// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"errors"
)

// CancelledFromAbove says a failure is the cancellation of the context the
// work ran under, not one some dependency reported: the error is a
// cancellation and the caller's own context is cancelled. That is a Slot
// stopped by its process stopping or its Query Group leaving, and it reads
// as ReasonSlotCancelled. A cancellation error under a live context is not
// this, and keeps whatever name it has.
func CancelledFromAbove(ctx context.Context, err error) bool {
	return ctx != nil && errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled)
}
