package actor

import (
	"context"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	promRemote "github.com/prometheus/prometheus/storage/remote"
)

type ActorQueryRangeStorage struct {
	QueryMaxRouting int
	Timeout         time.Duration
	Data            *prompb.QueryResult
}

func (s *ActorQueryRangeStorage) Querier(ctx context.Context, min, max int64) (storage.Querier, error) {
	return &Querier{
		ctx:        ctx,
		min:        time.Unix(min, 0),
		max:        time.Unix(max, 0),
		maxRouting: s.QueryMaxRouting,
		timeout:    s.Timeout,
		data:       s.Data,
	}, nil
}

type Querier struct {
	ctx        context.Context
	min        time.Time
	max        time.Time
	maxRouting int
	timeout    time.Duration
	data       *prompb.QueryResult
}

// Close implements storage.Querier.
func (q *Querier) Close() error {
	return nil
}

// LabelNames implements storage.Querier.
func (q *Querier) LabelNames(matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	panic("unimplemented")
}

// LabelValues implements storage.Querier.
func (q *Querier) LabelValues(name string, matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	panic("unimplemented")
}

// Select implements storage.Querier.
func (q *Querier) Select(_ bool, _ *storage.SelectHints, _ ...*labels.Matcher) storage.SeriesSet {
	qs := q.data
	set := promRemote.FromQueryResult(true, qs)
	sets := []storage.SeriesSet{set}
	if len(sets) == 0 {
		return storage.EmptySeriesSet()
	} else {
		return storage.NewMergeSeriesSet(sets, storage.ChainedSeriesMerge)
	}
}
