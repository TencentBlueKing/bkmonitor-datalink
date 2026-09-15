// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

// StateApplyChunkFacts describes one chunk of a chunked Runtime State
// admission, Runtime State apply or Plan gap apply. Index and Count are log
// attributes only and never become metric labels; the running totals let the
// last chunk's line summarise the whole apply of one Plan.
type StateApplyChunkFacts struct {
	Index int `json:"chunk_index"`
	Count int `json:"chunk_count"`
	// AppliedKeys and AppliedBytes cover this chunk and every earlier chunk
	// of the same apply; bytes are the encoded sizes measured at admission.
	AppliedKeys  int64 `json:"applied_keys"`
	AppliedBytes int64 `json:"applied_bytes"`
	// ElapsedMillis is the wall time from the first chunk start to the end of
	// this chunk, the measured apply time of the Plan so far.
	ElapsedMillis int64 `json:"elapsed_ms"`
}

func normalizeStateApplyChunk(o Observation) *StateApplyChunkFacts {
	f := o.StateApplyChunk
	if f == nil || o.Component != ComponentState {
		return nil
	}
	switch o.Stage {
	case StageStateAdmission, StageStateApplied, StageGapGuardCommitted:
	default:
		return nil
	}
	if f.Count <= 0 || f.Index < 0 || f.Index >= f.Count || f.AppliedKeys < 0 || f.AppliedBytes < 0 || f.ElapsedMillis < 0 {
		return nil
	}
	copy := *f
	return &copy
}
