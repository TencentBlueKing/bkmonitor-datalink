// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// LeaseHolderFacts is the lease a refused takeover ran into, as the
// ownership store answered it, on the takeover_completed line that failed:
// who holds it, under which owner epoch and session epoch, and until when on
// the store's clock. When the store did not say, Unknown names why instead,
// so that a refusal without a holder reads as one, not as a missing field.
type LeaseHolderFacts struct {
	OwnerID      string `json:"owner_id,omitempty"`
	OwnerEpoch   uint64 `json:"owner_epoch,omitempty"`
	SessionEpoch uint64 `json:"session_epoch,omitempty"`
	// DeadlineUnixMilli is the lease's deadline on the store's clock; zero for
	// a lease held under a worker session, which has none of its own.
	DeadlineUnixMilli int64  `json:"deadline_ms,omitempty"`
	Unknown           string `json:"unknown,omitempty"`
}

// Why a refused takeover's line names no holder, closed.
const (
	// LeaseHolderNoWorkerSession: this worker held no session yet, so the
	// takeover was refused here and the store was not asked.
	LeaseHolderNoWorkerSession = "no_worker_session"
	// LeaseHolderRefusedBeforeRead: the store refused before it read the
	// lease - the Query Group is not desired here, or its execution is
	// paused - so there is a refusal and no holder to name.
	LeaseHolderRefusedBeforeRead = "refused_before_read"
	// LeaseHolderNotAnswered: the store did not answer (an error on the way
	// to it, or a reply this build cannot read).
	LeaseHolderNotAnswered = "not_answered"
)
