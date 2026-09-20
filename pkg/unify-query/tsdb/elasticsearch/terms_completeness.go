package elasticsearch

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

// FTA results drive detection and recovery, so even successfully executed terms
// aggregations must prove that no buckets or counts were lost. Pointer fields
// distinguish explicit zero from missing/null metadata; -1 means unknown error.
func validateFTATermsCompleteness(raw []byte) error {
	var terms struct {
		OtherCount *int64 `json:"sum_other_doc_count"`
		CountError *int64 `json:"doc_count_error_upper_bound"`
		Buckets    []struct {
			CountError *int64 `json:"doc_count_error_upper_bound"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(raw, &terms); err != nil {
		return fmt.Errorf("invalid terms completeness metadata: %w", err)
	}
	if terms.OtherCount == nil || *terms.OtherCount != 0 || terms.CountError == nil || *terms.CountError != 0 {
		return fmt.Errorf("terms completeness requires zero discarded documents and count error")
	}
	for _, bucket := range terms.Buckets {
		if bucket.CountError == nil || *bucket.CountError != 0 {
			return fmt.Errorf("terms bucket completeness requires zero count error")
		}
	}
	return nil
}
