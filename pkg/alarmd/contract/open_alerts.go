// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

// OpenAlertSet is the consumer's set of open alerts as this process knows it.
//
// The consumer keeps one alert per series fingerprint and resolves it on any
// RECOVERY envelope, so an envelope for a series it holds no alert on is not
// a decision it can act on: it is written, read back and closed as an orphan,
// at the cost of a store write and several reads. A healthy series produces
// a RECOVERY result every cycle, which puts that cost at the population of
// healthy series per minute. The set lets the sender skip those envelopes.
//
// What the set says is membership only. It cannot say at which Level the
// alert stands, so it decides whether an envelope goes, never whether the
// series recovered; that stays with the Level results and the recovery gate.
//
// The implementation decides how it knows: an authoritative copy the consumer
// publishes, or what this process itself has sent while that copy is not
// available. Which of the two answered is the implementation's to report; a
// caller cannot tell from the answer, and must not try to.
type OpenAlertSet interface {
	// Contains reports whether the consumer holds an open alert on the series
	// under the strategy. It is a memory lookup and must not do I/O.
	Contains(tenantID, strategyID, fingerprint string) bool
}
