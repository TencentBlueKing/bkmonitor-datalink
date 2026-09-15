package http

import (
	"context"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const fieldSemanticsHeader = "X-Bk-Query-Field-Semantics"

func validateFieldSemanticsRequest(q *structured.QueryTs) error {
	for _, query := range q.QueryList {
		if query == nil || (query.FieldSemantics == "" && query.SourceConditions == nil) {
			continue
		}
		if query.FieldSemantics != metadata.FTAEventTagsV1 {
			return fmt.Errorf("unsupported field_semantics: %q", query.FieldSemantics)
		}
		if q.ResponseContract != "" {
			return fmt.Errorf("field_semantics does not support named outputs")
		}
		if len(query.AggregateMethodList) == 0 || query.TimeAggregation.Function == "" {
			return fmt.Errorf("field_semantics requires an aggregation query")
		}
	}
	return nil
}

// The response advertises semantics only after every physical route has actually
// completed. A successful empty aggregation qualifies; skipped routes do not.
func fieldSemanticsAcknowledgement(ctx context.Context, q *structured.QueryTs, result any) (bool, error) {
	opted := false
	for _, query := range q.QueryList {
		if query != nil && query.FieldSemantics != "" {
			opted = true
		}
	}
	if !opted {
		return false, nil
	}
	data, ok := result.(*PromData)
	if !ok || data == nil {
		return false, fmt.Errorf("field_semantics requires a time series response")
	}
	if data.IsPartial || (data.Status != nil && data.Status.Code != "") {
		return false, nil
	}
	for _, requested := range q.QueryList {
		if requested == nil || requested.FieldSemantics == "" {
			continue
		}
		count, complete := 0, true
		metadata.GetQueryReference(ctx).Range(requested.ReferenceName, func(query *metadata.Query) {
			count++
			complete = complete && query.FieldSemantics == metadata.FTAEventTagsV1 && query.FieldSemanticsExecution.Completed()
		})
		if count == 0 || !complete {
			return false, fmt.Errorf("field_semantics execution was not completed for every storage route")
		}
	}
	return true, nil
}

// FTA field semantics is a time-series aggregation contract only.
func rejectFieldSemantics(q *structured.QueryTs) error {
	for _, query := range q.QueryList {
		if query != nil && (query.FieldSemantics != "" || query.SourceConditions != nil) {
			return fmt.Errorf("field_semantics and source_conditions are only supported by /query/ts")
		}
	}
	return nil
}
