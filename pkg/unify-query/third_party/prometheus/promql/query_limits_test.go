package promql

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestCanBypassEngineTimeoutWithoutChangingOrdinaryQueries(t *testing.T) {
	engine := NewEngine(EngineOpts{MaxSamples: 1, Timeout: 0})
	query := engine.newTestQuery(func(ctx context.Context) error {
		_, deadline := ctx.Deadline()
		require.False(t, deadline)
		return contextDone(ctx, "test")
	})
	require.NoError(t, query.Exec(WithQueryLimitsDisabled(context.Background())).Err)
	query = engine.newTestQuery(func(ctx context.Context) error { return contextDone(ctx, "test") })
	var timeout ErrQueryTimeout
	require.ErrorAs(t, query.Exec(context.Background()).Err, &timeout)
}

func TestRequestCanBypassEngineSampleBudgetForInstantAndRange(t *testing.T) {
	test, err := NewTest(t, `load 1s
limit_metric 1 2 3 4 5
`)
	require.NoError(t, err)
	require.NoError(t, test.Run())
	defer test.Close()
	engine := NewEngine(EngineOpts{MaxSamples: 0, Timeout: time.Minute})
	for _, ranged := range []bool{false, true} {
		for _, unlimited := range []bool{false, true, false} {
			var query Query
			if ranged {
				query, err = engine.NewRangeQuery(test.Queryable(), nil, "limit_metric", time.Unix(0, 0), time.Unix(4, 0), time.Second)
			} else {
				query, err = engine.NewInstantQuery(test.Queryable(), nil, "limit_metric", time.Unix(4, 0))
			}
			require.NoError(t, err)
			ctx := context.Background()
			if unlimited {
				ctx = WithQueryLimitsDisabled(ctx)
			}
			result := query.Exec(ctx)
			if unlimited {
				require.NoError(t, result.Err)
				require.NotNil(t, result.Value)
			} else {
				var limit ErrTooManySamples
				require.ErrorAs(t, result.Err, &limit)
			}
			query.Close()
		}
	}
}

func TestUnlimitedEnginePreservesParentAndQueryCancellation(t *testing.T) {
	engine := NewEngine(EngineOpts{Timeout: 0})
	for _, cancelQuery := range []bool{false, true} {
		ctx, cancel := context.WithCancel(WithQueryLimitsDisabled(context.Background()))
		started := make(chan struct{})
		query := engine.newTestQuery(func(ctx context.Context) error { close(started); <-ctx.Done(); return contextDone(ctx, "test") })
		done := make(chan *Result, 1)
		go func() { done <- query.Exec(ctx) }()
		<-started
		if cancelQuery {
			query.Cancel()
		} else {
			cancel()
		}
		var canceled ErrQueryCanceled
		require.ErrorAs(t, (<-done).Err, &canceled)
		cancel()
	}
	ctx, cancel := context.WithDeadline(WithQueryLimitsDisabled(context.Background()), time.Unix(0, 0))
	defer cancel()
	query := engine.newTestQuery(func(context.Context) error { return errors.New("must not execute") })
	var timeout ErrQueryTimeout
	require.ErrorAs(t, query.Exec(ctx).Err, &timeout)
}

type limitsTestTracker struct{ inserted, deleted int }

func (*limitsTestTracker) GetMaxConcurrent() int                         { return 1 }
func (t *limitsTestTracker) Insert(context.Context, string) (int, error) { t.inserted++; return 0, nil }
func (t *limitsTestTracker) Delete(int)                                  { t.deleted++ }
func TestUnlimitedEngineBypassesOnlyItsOwnAdmission(t *testing.T) {
	tracker := &limitsTestTracker{}
	engine := NewEngine(EngineOpts{Timeout: time.Minute, ActiveQueryTracker: tracker})
	for _, unlimited := range []bool{false, true, false} {
		ctx := context.Background()
		if unlimited {
			ctx = WithQueryLimitsDisabled(ctx)
		}
		query := engine.newTestQuery(func(context.Context) error { return nil })
		require.NoError(t, query.Exec(ctx).Err)
	}
	require.Equal(t, 2, tracker.inserted)
	require.Equal(t, 2, tracker.deleted)
}
