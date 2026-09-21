// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ownership

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// RefusalReasons is every reason RefusalReason can name, in one place so a
// metric can pre-register them and a reader can know the whole vocabulary.
var RefusalReasons = []string{
	contract.ReasonOwnershipStaleFence, contract.ReasonOwnershipNotDesired,
	contract.ReasonOwnershipLeaseBusy, contract.ReasonContentScopeMoved,
}

// RefusalReason names, for observation, which of the store's four refusals
// an error is; ok is false for any other error, including nil. The store
// answers a fence check, an acquire, a renew or a fenced write with one of
// four typed errors, and every observing site had been reporting all four as
// internal_unknown -- so which one a deployment was seeing could only be read
// off the error sentence in a rate-limited log line. The four are told apart
// by errors.Is, so a wrapped refusal is still the refusal.
//
// Content moved is checked before stale: the store's scripts answer a moved
// scope with its own word and the callers treat it as its own case (re-read,
// do not release), and the two are never wrapped in one another, so the
// order only states which one wins if that ever changes.
func RefusalReason(err error) (reason string, ok bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrContentScopeMoved):
		return contract.ReasonContentScopeMoved, true
	case errors.Is(err, ErrNotDesired):
		return contract.ReasonOwnershipNotDesired, true
	case errors.Is(err, ErrLeaseBusy):
		return contract.ReasonOwnershipLeaseBusy, true
	case errors.Is(err, ErrStaleFence):
		return contract.ReasonOwnershipStaleFence, true
	}
	return "", false
}
