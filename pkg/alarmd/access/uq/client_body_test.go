package uq

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const bodyTestSeries = `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1700123456789,12.5]]}`

// partialBodyServer answers 200, after a delay, with the start of a body and
// one whole series, then either stalls until the request goes away or, cut,
// drops the connection under a body it declared longer.
func partialBodyServer(t *testing.T, cut bool, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(delay)
		head := `{"series":[` + bodyTestSeries + `,`
		if cut {
			hijacker, ok := writer.(http.Hijacker)
			if !ok {
				t.Error("setup: the test server cannot hand over its connection")
				return
			}
			connection, buffered, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer connection.Close()
			_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100000\r\n\r\n" + head)
			_ = buffered.Flush()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(head))
		writer.(http.Flusher).Flush()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
		case <-timer.C:
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// slowSink takes its time over every batch, as a delivery that does not keep
// up with the answer would, and then refuses it with err when one is set.
type slowSink struct {
	collectingSink
	delay time.Duration
	err   error
}

func (sink *slowSink) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	time.Sleep(sink.delay)
	if sink.err != nil {
		return sink.err
	}
	return sink.collectingSink.ConsumeProviderSeries(ctx, batch)
}

// The worker finds a failure's name, detail and timing by these methods; a
// body failure that stopped answering one would reach it unnamed again.
var _ interface {
	QueryFailure() (string, string)
	QueryFailureDetail() string
	QueryFailureTiming() *execution.AttemptTiming
} = (*bodyFailureError)(nil)

func bodyFailureOf(t *testing.T, err error) *bodyFailureError {
	t.Helper()
	var failure *bodyFailureError
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want a named body failure", err)
	}
	return failure
}

// A query whose answer began and whose body did not arrive in full names
// itself, with its timing, rather than reading as an unclassified internal
// error. A body that stopped coming is the backend's timeout, body=timeout,
// and spends almost none of the time locally - also when the answer was slow
// to begin, which is waiting on the backend too. A sink that did not keep up
// ran the time out on alarmd's side: delivery=timeout, not the provider's,
// most of it local. A dropped connection is the backend's unavailability.
// Series handed on before the failure stay delivered - it is still an error,
// not a completion.
func TestAQueryWhoseBodyStopsPartwayNamesItself(t *testing.T) {
	whole := func(timing *execution.AttemptTiming, attempt execution.QueryAttempt) bool {
		return timing.SettleMillis+timing.StartLateMillis+timing.BudgetMillis == attempt.DeadlineUnixMilli-attempt.BudgetStartUnixMilli
	}
	slot := func() execution.QueryAttempt {
		attempt := validAttempt(t)
		now := time.Now()
		attempt.BudgetStartUnixMilli = now.Add(-time.Second).UnixMilli()
		attempt.ReadyAtUnixMilli = attempt.BudgetStartUnixMilli
		attempt.DeadlineUnixMilli = now.Add(300 * time.Millisecond).UnixMilli()
		return attempt
	}

	stalled := partialBodyServer(t, false, 0)
	client, err := NewClient(stalled.URL, "alarmd-shadow", stalled.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt := slot()
	sink := &collectingSink{}
	_, err = client.Execute(context.Background(), attempt, sink)
	failure := bodyFailureOf(t, err)
	category, code := failure.QueryFailure()
	timing := failure.QueryFailureTiming()
	if category != "provider_transport" || code != "QUERY_TIMEOUT" || failure.QueryFailureDetail() != "body=timeout" || timing == nil {
		t.Fatalf("a body that stopped coming = %s/%s %q timing %+v, want provider_transport/QUERY_TIMEOUT body=timeout, timed",
			category, code, failure.QueryFailureDetail(), timing)
	}
	if !whole(timing, attempt) || timing.ElapsedMillis < timing.BudgetMillis-50 || timing.LocalMillis < 0 || timing.LocalMillis > 100 {
		t.Fatalf("timing = %+v, want the budget used up waiting on the backend, little of it local", *timing)
	}
	if len(sink.batches) != 1 {
		t.Fatalf("delivered %d batches, want the one series that arrived", len(sink.batches))
	}

	late := partialBodyServer(t, false, 200*time.Millisecond)
	client, err = NewClient(late.URL, "alarmd-shadow", late.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt = slot()
	attempt.DeadlineUnixMilli = time.Now().Add(500 * time.Millisecond).UnixMilli()
	_, err = client.Execute(context.Background(), attempt, &collectingSink{})
	failure = bodyFailureOf(t, err)
	timing = failure.QueryFailureTiming()
	if failure.QueryFailureDetail() != "body=timeout" || timing == nil || !whole(timing, attempt) ||
		timing.ElapsedMillis < 400 || timing.LocalMillis < 0 || timing.LocalMillis > 50 {
		t.Fatalf("an answer slow to begin, then stalled = %q timing %+v, want the backend's timeout with nothing local", failure.QueryFailureDetail(), timing)
	}

	slow := fixtureClient(t, http.StatusOK, `{"series":[`+bodyTestSeries+`],"is_partial":false}`, DefaultLimits())
	attempt = slot()
	_, err = slow.Execute(context.Background(), attempt, &slowSink{delay: 500 * time.Millisecond})
	failure = bodyFailureOf(t, err)
	timing = failure.QueryFailureTiming()
	if category, code := failure.QueryFailure(); category != "other" || code != "OTHER" || failure.QueryFailureDetail() != "delivery=timeout" ||
		timing == nil || !whole(timing, attempt) || timing.LocalMillis < 400 || timing.LocalMillis > timing.ElapsedMillis {
		t.Fatalf("a sink that did not keep up = %s/%s %q timing %+v, want alarmd's own delivery timeout, spent mostly local",
			category, code, failure.QueryFailureDetail(), timing)
	}

	cut := partialBodyServer(t, true, 0)
	client, err = NewClient(cut.URL, "alarmd-shadow", cut.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt = slot()
	attempt.DeadlineUnixMilli = time.Now().Add(time.Minute).UnixMilli()
	_, err = client.Execute(context.Background(), attempt, &collectingSink{})
	failure = bodyFailureOf(t, err)
	if category, code := failure.QueryFailure(); category != "provider_transport" || code != "QUERY_UNAVAILABLE" ||
		failure.QueryFailureDetail() != "body=eof" || failure.QueryFailureTiming() == nil || !whole(failure.QueryFailureTiming(), attempt) {
		t.Fatalf("a connection dropped under its body = %s/%s %q timing %+v, want provider_transport/QUERY_UNAVAILABLE body=eof, timed",
			category, code, failure.QueryFailureDetail(), failure.QueryFailureTiming())
	}
	if !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("the named failure lost its own text: %v", err)
	}
}

// Only a transport failure of the body is named. A body that breaks the
// wire contract is a different failure - one cut short within the length it
// declared, or empty, ends where the server ended it - a response budget
// keeps its own name, and a caller that gave up keeps its own error, as each
// does before the answer begins.
func TestOnlyABodysTransportFailureIsNamed(t *testing.T) {
	var failure *bodyFailureError
	for name, body := range map[string]string{"malformed": `{"series":[{"name":`, "empty": ""} {
		client := fixtureClient(t, http.StatusOK, body, DefaultLimits())
		if _, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{}); err == nil || errors.As(err, &failure) {
			t.Fatalf("a %s body = %v, want its own error unnamed", name, err)
		}
	}

	limits := DefaultLimits()
	limits.MaxBodyBytes, limits.MaxSeriesBytes = 64, 64
	oversized := fixtureClient(t, http.StatusOK, `{"series":[`+bodyTestSeries+`]}`, limits)
	wantLimitCompletion(t, oversized, "response=limit_response_bytes")

	refusing := fixtureClient(t, http.StatusOK, `{"series":[`+bodyTestSeries+`],"is_partial":false}`, DefaultLimits())
	past := validAttempt(t)
	past.DeadlineUnixMilli = time.Now().Add(100 * time.Millisecond).UnixMilli()
	if _, err := refusing.Execute(context.Background(), past, &slowSink{delay: 300 * time.Millisecond, err: errors.New("sink refused")}); err == nil ||
		err.Error() != "sink refused" || errors.As(err, &failure) {
		t.Fatalf("a sink refusing after the deadline = %v, want its own error unnamed", err)
	}

	stalled := partialBodyServer(t, false, 0)
	client, err := NewClient(stalled.URL, "alarmd-shadow", stalled.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	attempt.DeadlineUnixMilli = time.Now().Add(time.Minute).UnixMilli()
	caller, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := client.Execute(caller, attempt, &collectingSink{}); err == nil || errors.As(err, &failure) {
		t.Fatalf("a caller that gave up = %v, want its own error unnamed", err)
	}
}

// Who held the time when the deadline passed decides the name, and the read
// that failed says so by when it began and returned. Begun before the
// deadline and ended by it, whatever error the read ended with, alarmd was
// waiting on the backend: its timeout. Begun after, alarmd was busy when the
// time ran out and the read only found out: alarmd's delivery. A connection
// that broke before the deadline is the backend's unavailability, by its
// own class.
func TestABodyDeadlineIsNamedForWhoHeldTheTime(t *testing.T) {
	client := &Client{now: time.Now}
	deadline := time.Now().Add(-time.Second)
	expired, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	attempt := queryIdentity{Budget: queryBudget{StartUnixMilli: deadline.Add(-time.Minute).UnixMilli(), ReadyAtUnixMilli: deadline.Add(-time.Minute).UnixMilli(),
		DeadlineUnixMilli: deadline.UnixMilli()}}
	started := deadline.Add(-10 * time.Second)
	for name, testCase := range map[string]struct {
		began, returned        time.Duration
		category, code, detail string
	}{
		"a read the deadline ended":       {-time.Second, 10 * time.Millisecond, "provider_transport", "QUERY_TIMEOUT", "body=timeout"},
		"a read begun after the deadline": {10 * time.Millisecond, 20 * time.Millisecond, "other", "OTHER", "delivery=timeout"},
		"a connection broken before it":   {-2 * time.Second, -time.Second, "provider_transport", "QUERY_UNAVAILABLE", "body=connection_reset"},
	} {
		body := &countingReader{failed: syscall.ECONNRESET, failedBegan: deadline.Add(testCase.began), failedAt: deadline.Add(testCase.returned)}
		err := client.bodyFailure(context.Background(), expired, attempt, fmt.Errorf("alarmd access uq: decode series payload: %w", syscall.ECONNRESET),
			body, started, started.Add(time.Second))
		failure := bodyFailureOf(t, err)
		if category, code := failure.QueryFailure(); category != testCase.category || code != testCase.code || failure.QueryFailureDetail() != testCase.detail {
			t.Errorf("%s = %s/%s %q, want %s/%s %q", name, category, code, failure.QueryFailureDetail(), testCase.category, testCase.code, testCase.detail)
		}
	}
	// With no deadline at all, nothing expired: a broken connection is the
	// backend's unavailability whenever its read returned.
	body := &countingReader{failed: syscall.ECONNRESET, failedBegan: time.Now(), failedAt: time.Now()}
	err := client.bodyFailure(context.Background(), context.Background(), attempt, fmt.Errorf("decode: %w", syscall.ECONNRESET), body, started, started)
	if failure := bodyFailureOf(t, err); failure.QueryFailureDetail() != "body=connection_reset" {
		t.Errorf("a reset under no deadline = %q, want body=connection_reset", failure.QueryFailureDetail())
	}
}

// failingReader waits, then fails, as a body read the connection breaks
// under does.
type failingReader struct{ wait time.Duration }

func (reader failingReader) Read([]byte) (int, error) {
	time.Sleep(reader.wait)
	return 0, syscall.ECONNRESET
}

// The reader keeps what the name is decided by: the first read that failed,
// when it began and when it returned, and the time spent inside reads.
func TestTheBodyReaderKeepsWhenItsFailingReadBeganAndReturned(t *testing.T) {
	body := &countingReader{reader: failingReader{wait: 30 * time.Millisecond}, now: time.Now}
	before := time.Now()
	if _, err := body.Read(make([]byte, 8)); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("read = %v, want the reset", err)
	}
	if body.failed != syscall.ECONNRESET || body.failedBegan.Before(before) || body.failedAt.Sub(body.failedBegan) < 30*time.Millisecond ||
		body.waited < 30*time.Millisecond {
		t.Fatalf("reader = failed %v began +%v returned +%v waited %v, want the reset, returned 30 ms after it began, all of it waited",
			body.failed, body.failedBegan.Sub(before), body.failedAt.Sub(before), body.waited)
	}
}
