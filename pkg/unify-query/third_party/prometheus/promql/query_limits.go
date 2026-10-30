package promql

import "context"

type queryLimitsDisabledKey struct{}

// WithQueryLimitsDisabled bypasses internal timeout, sample and admission limits
// for this request. Parent deadlines and cancellation still apply.
func WithQueryLimitsDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, queryLimitsDisabledKey{}, true)
}

func QueryLimitsDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(queryLimitsDisabledKey{}).(bool)
	return disabled
}
