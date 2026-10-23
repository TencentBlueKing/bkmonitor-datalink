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
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// observeRuntime hands one observation to the runtime's observer. A panic
// is recovered and counted: inside the fan-out under the member that raised
// it, and anywhere before it - normalization, an observer handed over
// directly - under the entry.
func observeRuntime(ctx context.Context, observer observability.Observer, observation observability.Observation) {
	if observer == nil {
		return
	}
	defer observability.RecoverObserverPanic(observability.ObserverEntry)
	observer.Observe(ctx, observation)
}
