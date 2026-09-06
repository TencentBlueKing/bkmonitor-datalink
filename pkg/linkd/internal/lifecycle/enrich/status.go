// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import "linkd/internal/domain"

func aggregateStatus(results []ProcessorResult) domain.EnrichStatus {
	if len(results) == 0 {
		return domain.EnrichStatusSucceeded
	}
	succeeded, failed := 0, 0
	skipped := 0
	for _, result := range results {
		switch result.Status {
		case domain.EnrichStatusSucceeded:
			succeeded++
		case domain.EnrichStatusFailed:
			failed++
		case domain.EnrichStatusSkipped:
			skipped++
		default:
			return domain.EnrichStatusPartial
		}
	}
	if skipped == len(results) {
		return domain.EnrichStatusSkipped
	}
	applicable := len(results) - skipped
	if succeeded == applicable {
		return domain.EnrichStatusSucceeded
	}
	if failed == applicable {
		return domain.EnrichStatusFailed
	}
	return domain.EnrichStatusPartial
}

func aggregateEntries(entries []ProcessorEntry) domain.EnrichStatus {
	results := make([]ProcessorResult, 0, len(entries))
	for _, entry := range entries {
		for _, envelope := range entry {
			results = append(results, ProcessorResult{Status: envelope.Status})
		}
	}
	return aggregateStatus(results)
}
