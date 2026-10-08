package uq

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func failedAttempt(t *testing.T, completion execution.ProviderCompletion, err error) execution.RouteAttemptFact {
	t.Helper()
	if err != nil || completion.Completeness != execution.CompletenessUnavailable || len(completion.RouteFacts.Attempts) != 1 ||
		completion.RouteFacts.Attempts[0].Result != execution.RouteAttemptFailed {
		t.Fatalf("completion=%+v error=%v, want one failed UNAVAILABLE attempt", completion, err)
	}
	return completion.RouteFacts.Attempts[0]
}

func TestClientRecordsHTTPStatusDetailWithoutBody(t *testing.T) {
	client := fixtureClient(t, http.StatusServiceUnavailable, `{"error":"table secret_table does not exist"}`, DefaultLimits())
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	attempt := failedAttempt(t, completion, err)
	if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) || attempt.Detail != "http_status=503" {
		t.Fatalf("attempt=%+v, want QUERY_UNAVAILABLE http_status=503", attempt)
	}
	if completion.DataState != execution.DataStateUnknown {
		t.Fatalf("non-200 changed data state: %+v", completion)
	}
}

func TestClientRecordsConnectionRefusedDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	httpClient := server.Client()
	server.Close()
	client, err := NewClient(endpoint, "alarmd-shadow", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	attempt := failedAttempt(t, completion, err)
	if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) || attempt.Detail != "transport=connection_refused" {
		t.Fatalf("attempt=%+v, want QUERY_UNAVAILABLE transport=connection_refused", attempt)
	}
}

func TestClientRecordsClosedConnectionDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Fatal("response writer cannot hijack")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "alarmd-shadow", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	attempt := failedAttempt(t, completion, err)
	if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) {
		t.Fatalf("attempt=%+v, want QUERY_UNAVAILABLE", attempt)
	}
	switch attempt.Detail {
	case "transport=eof", "transport=connection_reset":
	default:
		t.Fatalf("closed connection detail=%q, want eof or connection_reset", attempt.Detail)
	}
}

func TestClientRecordsTimeoutDetail(t *testing.T) {
	server := stalledResponseServer(t)
	client, err := NewClient(server.URL, "alarmd-shadow", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	attempt.DeadlineUnixMilli = time.Now().Add(10 * time.Millisecond).UnixMilli()
	completion, err := client.Execute(context.Background(), attempt, &collectingSink{})
	fact := failedAttempt(t, completion, err)
	if fact.ReasonCode != execution.ReasonCode(contract.ReasonQueryTimeout) || fact.Detail != "transport=timeout" {
		t.Fatalf("attempt=%+v, want QUERY_TIMEOUT transport=timeout", fact)
	}
}

// A Slot query that does not come back splits its budget where its window
// could be read and where its request went out: the settling wait, how late
// after it the query began, what it had left to its deadline, and what it
// used - the first three adding up to the whole budget to the millisecond.
// Begun two seconds late with a few hundred milliseconds left, the timeout
// used what it was given; begun past its deadline, it had less than nothing,
// and says so rather than zero. A backend that refused at once used almost
// nothing of the attempt's budget, measured to the attempt's deadline and
// not a caller's; a recovery that arrives after its window was readable had
// no settling wait. A read that is not a Slot's reports no timing.
func TestAFailedSlotQuerySplitsItsBudgetAtItsStart(t *testing.T) {
	whole := func(timing *execution.AttemptTiming, attempt execution.QueryAttempt) bool {
		return timing.SettleMillis+timing.StartLateMillis+timing.BudgetMillis == attempt.DeadlineUnixMilli-attempt.BudgetStartUnixMilli
	}
	server := stalledResponseServer(t)
	client, err := NewClient(server.URL, "alarmd-shadow", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	now := time.Now()
	attempt.BudgetStartUnixMilli = now.Add(-3 * time.Second).UnixMilli()
	attempt.ReadyAtUnixMilli = attempt.BudgetStartUnixMilli + 1_000
	attempt.DeadlineUnixMilli = now.Add(300 * time.Millisecond).UnixMilli()
	completion, err := client.Execute(context.Background(), attempt, &collectingSink{})
	fact := failedAttempt(t, completion, err)
	timing := fact.Timing
	if fact.Detail != "transport=timeout" || timing == nil {
		t.Fatalf("attempt=%+v, want a timeout with its timing", fact)
	}
	if !whole(timing, attempt) || timing.SettleMillis != 1_000 || timing.StartLateMillis < 2_000 || timing.StartLateMillis > 3_500 ||
		timing.BudgetMillis <= 0 || timing.BudgetMillis > 300 ||
		timing.ElapsedMillis < timing.BudgetMillis-50 || timing.ElapsedMillis > timing.BudgetMillis+1000 {
		t.Fatalf("timing = %+v, want 1 s settling, about 2 s late, at most 300 ms given and all of it used, adding up to the budget", *timing)
	}

	late := attempt
	late.BudgetStartUnixMilli = time.Now().Add(-10 * time.Second).UnixMilli()
	late.ReadyAtUnixMilli = late.BudgetStartUnixMilli
	late.DeadlineUnixMilli = time.Now().Add(-2 * time.Second).UnixMilli()
	completion, err = client.Execute(context.Background(), late, &collectingSink{})
	fact = failedAttempt(t, completion, err)
	if fact.Detail != "transport=timeout" || fact.Timing == nil || !whole(fact.Timing, late) || fact.Timing.BudgetMillis > -2_000 {
		t.Fatalf("a query begun past its deadline = %+v timing %+v, want a budget below zero adding up to the whole", fact, fact.Timing)
	}

	refused := fixtureClient(t, http.StatusServiceUnavailable, "", DefaultLimits())
	caller, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	attempt.BudgetStartUnixMilli = time.Now().Add(-time.Second).UnixMilli()
	attempt.ReadyAtUnixMilli = attempt.BudgetStartUnixMilli
	attempt.DeadlineUnixMilli = time.Now().Add(time.Minute).UnixMilli()
	completion, err = refused.Execute(caller, attempt, &collectingSink{})
	fact = failedAttempt(t, completion, err)
	if fact.Detail != "http_status=503" || fact.Timing == nil || !whole(fact.Timing, attempt) || fact.Timing.BudgetMillis < 50_000 || fact.Timing.ElapsedMillis > 5_000 {
		t.Fatalf("an immediate refusal = %+v timing %+v, want the attempt's large budget barely used", fact, fact.Timing)
	}

	recovery := attempt
	recovery.ReadyAtUnixMilli = recovery.BudgetStartUnixMilli - 30_000
	completion, err = refused.Execute(context.Background(), recovery, &collectingSink{})
	fact = failedAttempt(t, completion, err)
	if fact.Timing == nil || !whole(fact.Timing, recovery) || fact.Timing.SettleMillis != 0 || fact.Timing.StartLateMillis < 1_000 {
		t.Fatalf("a recovery arriving after its window was readable = %+v, want no settling wait", fact.Timing)
	}

	// A diagnostic or recheck read runs under a deadline of its own, and
	// with no Slot there is no budget to time it against.
	bounded, cancelBounded := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBounded()
	completion, err = refused.execute(bounded, bounded, queryIdentity{Spec: attempt.Spec, AttemptNo: 1}, &collectingSink{}, nil)
	if fact := failedAttempt(t, completion, err); fact.Timing != nil {
		t.Fatalf("a read that is not a Slot's reported timing %+v", *fact.Timing)
	}
}

func TestClientRecordsResponseContractDetailForMissingIsPartial(t *testing.T) {
	body := `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,12.5]]}]}`
	client := fixtureClient(t, http.StatusOK, body, DefaultLimits())
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	if err != nil || completion.Completeness != execution.CompletenessUnavailable || len(completion.RouteFacts.Attempts) != 1 {
		t.Fatalf("completion=%+v error=%v", completion, err)
	}
	if got := completion.RouteFacts.Attempts[0].Detail; got != "response=is_partial_missing" {
		t.Fatalf("detail=%q, want response=is_partial_missing", got)
	}
}

func TestClassifyTransportFailureUsesErrorTypesOnly(t *testing.T) {
	wrap := func(err error) error {
		return &url.Error{Op: "Post", URL: "https://user:secret@example.test/query/ts", Err: err}
	}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, execution.TransportFailureOther},
		{"deadline", wrap(context.DeadlineExceeded), execution.TransportFailureTimeout},
		{"dns", wrap(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "uq.example.test"}}), execution.TransportFailureDNS},
		{"tls", wrap(x509.UnknownAuthorityError{}), execution.TransportFailureTLS},
		{"refused", wrap(&net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}), execution.TransportFailureConnectionRefused},
		{"reset", wrap(&net.OpError{Op: "read", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}), execution.TransportFailureConnectionReset},
		{"eof", wrap(io.EOF), execution.TransportFailureEOF},
		{"unexpected eof", wrap(io.ErrUnexpectedEOF), execution.TransportFailureEOF},
		{"net timeout", wrap(&net.OpError{Op: "read", Err: timeoutError{}}), execution.TransportFailureTimeout},
		{"other", wrap(errors.New("something else")), execution.TransportFailureOther},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyTransportFailure(test.err); got != test.want {
				t.Fatalf("classifyTransportFailure(%v) = %q, want %q", test.err, got, test.want)
			}
			if detail := execution.TransportRouteDetail(classifyTransportFailure(test.err)); detail != "transport="+test.want {
				t.Fatalf("detail=%q", detail)
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
