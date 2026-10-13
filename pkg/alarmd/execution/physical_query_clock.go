// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// PhysicalQueryClock is what one physical query had of its time and what it
// used: the budget from the moment it was sent to its deadline, and how long
// it took to come back - answered, timed out or failed. A Slot carries the
// slowest PRIMARY query's (SlotExecutionResult.PrimaryQueryClock), which is
// what says how far a backend that answered was from the budget it had.
//
// ReadyAtUnixMilli is when the query became ready to send, as planned. A
// query sent after it waited - for a permit, or for the execution ahead of it
// - and the clock's budget is what that wait left.
type PhysicalQueryClock struct {
	BudgetMillis     int64 `json:"budget_ms"`
	ElapsedMillis    int64 `json:"elapsed_ms"`
	ReadyAtUnixMilli int64 `json:"ready_at_ms,omitempty"`
}
