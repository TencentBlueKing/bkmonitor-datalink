package uq

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A success-rate strategy is written as `(1 - (a + b) / c) * 100 or vector(100)`
// so that "no failures" evaluates to 100. When none of the sub-queries route,
// UQ answers with both halves of the truth: the constant series the expression
// defines, and the code saying the tables were not found. alarmd read only the
// code and discarded the series, so two such strategies produced nothing for
// about 34 hours while Python evaluated them every cycle from the same
// response.
func TestExpressionFallbackSeriesSurvivesADataExistenceStatus(t *testing.T) {
	fallback := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,100]]}`
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(
		context.Background(),
		strings.NewReader(`{"series":[`+fallback+`],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`),
		validAttempt(t), sink)
	if err != nil {
		t.Fatalf("decode() error: %v", err)
	}
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData {
		t.Fatalf("completion=%+v, want the delivered series treated as data", completion)
	}
	if len(sink.batches) != 1 || completion.Stats.Series != 1 || completion.Stats.Records != 1 {
		t.Fatalf("batches=%d stats=%+v, want the one fallback series", len(sink.batches), completion.Stats)
	}
}

// The other half of the same condition: the code alone, with nothing delivered,
// still completes as UNAVAILABLE. That case is not a strategy answering by
// itself - it is a query that found nothing, and saying "unavailable" about it
// is correct.
func TestDataExistenceStatusWithoutSeriesStaysUnavailable(t *testing.T) {
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(
		context.Background(),
		strings.NewReader(`{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`),
		validAttempt(t), sink)
	attempt := failedAttempt(t, completion, err)
	if attempt.Detail != "response=status_space_table_id_field_is_not_exists" {
		t.Fatalf("attempt=%+v, want the unavailable completion kept", attempt)
	}
}

// A storage failure that happened to deliver a series is still a storage
// failure. Only codes on the closed data-existence list may keep their series;
// this is the assertion that stops the list from being read as "any code, as
// long as something arrived".
func TestDeliveredSeriesDoesNotRescueAnUnhealthyQuery(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(
		context.Background(),
		strings.NewReader(`{"series":[`+series+`],"status":{"code":"QUERY_TS_STORAGE_TIMEOUT"},"is_partial":false}`),
		validAttempt(t), sink)
	attempt := failedAttempt(t, completion, err)
	if attempt.Detail != "response=status_query_ts_storage_timeout" {
		t.Fatalf("attempt=%+v, want the storage timeout still unavailable", attempt)
	}
}

// A response that lost some of its routes reports QUERY_TS_PARTIAL as its final
// code, because UQ writes that status after the fan-out and the routing
// statuses while the query is still being built - one status slot, last writer
// wins. So a genuinely partial answer never reaches the data-existence list at
// all, and stays PARTIAL. That ordering is the whole reason the list is safe,
// and until now it lived only in a comment.
func TestPartialMergeNeverReachesTheDataExistenceList(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(
		context.Background(),
		strings.NewReader(`{"series":[`+series+`],"status":{"code":"QUERY_TS_PARTIAL"},"is_partial":false}`),
		validAttempt(t), sink)
	if err != nil {
		t.Fatalf("decode() error: %v", err)
	}
	if completion.Completeness != execution.CompletenessPartial {
		t.Fatalf("completion=%+v, want a partial merge kept partial", completion)
	}
}

// The passed-through code stays on the succeeded attempt. It is the only place
// it survives once the completion is no longer UNAVAILABLE, and without it a
// result table that was really deleted would route nowhere, the fallback would
// answer, and the strategy would report itself healthy with nothing to look at.
func TestPassedThroughStatusStaysOnTheAttempt(t *testing.T) {
	fallback := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,100]]}`
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(
		context.Background(),
		strings.NewReader(`{"series":[`+fallback+`],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`),
		validAttempt(t), sink)
	if err != nil {
		t.Fatalf("decode() error: %v", err)
	}
	attempts := completion.RouteFacts.Attempts
	if len(attempts) != 1 || attempts[0].Result != execution.RouteAttemptSucceeded ||
		attempts[0].Detail != "response=status_space_table_id_field_is_not_exists" {
		t.Fatalf("attempts=%+v, want the passed-through code kept on a succeeded attempt", attempts)
	}
}

// A healthy response carries no detail. Without this the previous test would
// pass just as well against a client that stamped the detail unconditionally,
// and the counter built on it would count every query.
func TestHealthyResponseCarriesNoStatusDetail(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	client := &Client{limits: DefaultLimits(), now: time.Now}
	sink := &collectingSink{}
	completion, err := client.decode(
		context.Background(),
		strings.NewReader(`{"series":[`+series+`],"is_partial":false}`),
		validAttempt(t), sink)
	if err != nil {
		t.Fatalf("decode() error: %v", err)
	}
	if detail := completion.RouteFacts.Attempts[0].Detail; detail != "" {
		t.Fatalf("healthy attempt detail=%q, want empty", detail)
	}
}

// Both outcomes are recorded as data, not left to be recovered from the detail
// string. Counting only the allowed one would make "this code started
// appearing" and "this code started being allowed" the same number, and those
// are the two things that change together the moment a code is first allowed.
func TestBothStatusOutcomesAreRecordedAsFacts(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,100]]}`
	for _, one := range []struct {
		name        string
		payload     string
		wantCode    string
		wantAllowed bool
	}{
		{
			name:     "allowed",
			payload:  `{"series":[` + series + `],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`,
			wantCode: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS", wantAllowed: true,
		},
		{
			name:     "same code without a series",
			payload:  `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"},"is_partial":false}`,
			wantCode: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS", wantAllowed: false,
		},
		{
			name:     "unhealthy query with a series",
			payload:  `{"series":[` + series + `],"status":{"code":"QUERY_TS_STORAGE_TIMEOUT"},"is_partial":false}`,
			wantCode: "QUERY_TS_STORAGE_TIMEOUT", wantAllowed: false,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			client := &Client{limits: DefaultLimits(), now: time.Now}
			completion, _ := client.decode(context.Background(), strings.NewReader(one.payload), validAttempt(t), &collectingSink{})
			fact := completion.RouteFacts.Status
			if fact == nil {
				t.Fatalf("completion=%+v, want the status recorded as a fact", completion.RouteFacts)
			}
			if fact.Code != one.wantCode || fact.Allowed != one.wantAllowed {
				t.Fatalf("status fact=%+v, want code %q allowed=%v", fact, one.wantCode, one.wantAllowed)
			}
		})
	}
}

// A response with no status records no fact. Without this the counter built on
// it would have a bucket for every healthy query.
func TestHealthyResponseRecordsNoStatusFact(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,1]]}`
	client := &Client{limits: DefaultLimits(), now: time.Now}
	completion, err := client.decode(context.Background(),
		strings.NewReader(`{"series":[`+series+`],"is_partial":false}`), validAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatalf("decode() error: %v", err)
	}
	if completion.RouteFacts.Status != nil {
		t.Fatalf("status fact=%+v, want none for a healthy response", completion.RouteFacts.Status)
	}
}
