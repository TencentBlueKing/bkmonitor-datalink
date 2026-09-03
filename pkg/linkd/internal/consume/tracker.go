// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import "time"

type trackedDelivery struct {
	delivery   Delivery
	attempt    int
	firstAt    time.Time
	terminal   bool
	processing bool
}

type laneState struct {
	queue    []*trackedDelivery
	settling bool
	blocked  bool
	inflight int
}

type settleBatch struct {
	lane        string
	entries     []*trackedDelivery
	receipts    []Receipt
	nextAttempt time.Time
	startedAt   time.Time
}
