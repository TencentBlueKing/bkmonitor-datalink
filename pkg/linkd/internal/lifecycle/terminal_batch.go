// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"

	"linkd/internal/domain"
	"linkd/internal/suppressioncleanup"
)

func (p *Processor) clearSuppressions(ctx context.Context, alerts []domain.Alert) []error {
	results := make([]error, len(alerts))
	if p.dependency != nil {
		if batch, ok := p.dependency.(interface {
			ClearDependencies(context.Context, []domain.Alert) []error
		}); ok {
			results = batch.ClearDependencies(ctx, alerts)
		} else {
			for i, a := range alerts {
				results[i] = p.dependency.ClearDependency(ctx, a.BKTenantID, a.AlertID)
			}
		}
	}
	if p.suppressor == nil {
		return results
	}
	causes, positions := []suppressioncleanup.Cause{}, []int{}
	for i, a := range alerts {
		if results[i] == nil {
			causes = append(causes, suppressioncleanup.Cause{TenantID: a.BKTenantID, SourceID: a.EventSourceID, Fingerprint: a.Fingerprint, Trigger: "alert_terminal", AlertID: a.AlertID, Revision: a.Revision, Status: a.Status})
			positions = append(positions, i)
		}
	}
	if len(causes) == 0 {
		return results
	}
	if batch, ok := p.suppressor.(interface {
		ClearBatch(context.Context, []suppressioncleanup.Cause) []error
	}); ok {
		errs := batch.ClearBatch(ctx, causes)
		for j, i := range positions {
			results[i] = errs[j]
		}
	} else {
		for j, i := range positions {
			results[i] = p.suppressor.Clear(ctx, causes[j])
		}
	}
	return results
}
