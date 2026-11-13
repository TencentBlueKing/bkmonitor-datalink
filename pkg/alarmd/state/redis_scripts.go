// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"

// The scripts this package defines as constants, by the names their cost reads
// under (redisscript).
var (
	_ = redisscript.Register("state_compare_and_set", compareAndSetScript)
	_ = redisscript.Register("state_fenced_batch_write", fencedBatchWriteScript)
	_ = redisscript.Register("state_renew_if_below", renewIfBelowScript)
	_ = redisscript.Register("state_apply_hash_delta", applyHashDeltaScript)
	_ = redisscript.Register("state_record_hash_field", recordHashFieldScript)
	_ = redisscript.Register("state_stage_hash_segment", stageHashSegmentScript)
	_ = redisscript.Register("state_swap_staged_hash", swapStagedHashScript)
	_ = redisscript.Register("state_discard_staged_hash", discardStagedHashScript)
)
