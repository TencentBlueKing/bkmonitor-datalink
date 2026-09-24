// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// RetainedShareApproachPercent is how much of its one-object share of the
// retained pool an object's latest completed Slot has to hold to be listed
// as approaching it.
//
// The share is a wall that stops a strategy whole: a Slot past it is refused
// by name every round, and nothing before that says it is coming. The
// largest strategy on a live deployment held 86 percent of its share at the
// median and 91 at p99 for a day, with no line anywhere, and ten percent more
// series would have stopped it. Ninety-five leaves room to act between the
// warning and the wall, and sits above the p99 of that strategy as it is
// today, so the line opens when it grows rather than on every round now.
const RetainedShareApproachPercent = 95

// RetainedShareApproaching is the rule, decided on the bytes: a completed
// Slot holding at least RetainedShareApproachPercent of its one-object share.
// A share of zero is no share - a producer that did not carry one - and is
// never approaching.
func RetainedShareApproaching(retained, share uint64) bool {
	return share > 0 && retained*100 >= share*RetainedShareApproachPercent
}
