// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// pulledReader is an answer read from parts, some of them long runs of one
// byte made as they are read, and counts what the decoder pulled from it:
// what it pulled is the most it could have buffered.
type pulledReader struct {
	reader io.Reader
	pulled int64
}

func answerOf(parts ...any) *pulledReader {
	readers := make([]io.Reader, 0, len(parts))
	for _, part := range parts {
		switch part := part.(type) {
		case string:
			readers = append(readers, strings.NewReader(part))
		case int64:
			readers = append(readers, io.LimitReader(repeatedByte('x'), part))
		}
	}
	return &pulledReader{reader: io.MultiReader(readers...)}
}

func (reader *pulledReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.pulled += int64(count)
	return count, err
}

type repeatedByte byte

func (b repeatedByte) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(b)
	}
	return len(buffer), nil
}

// countingSeriesSink keeps only how many series reached it.
type countingSeriesSink struct{ series uint64 }

func (sink *countingSeriesSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	sink.series += batch.Delivery.Series
	return nil
}

// decodeWindowed decodes an answer with limits and says how it completed
// and how many series it delivered.
func decodeWindowed(t *testing.T, limits Limits, answer io.Reader) (execution.ProviderCompletion, uint64) {
	t.Helper()
	client := &Client{limits: limits, now: time.Now}
	sink := &countingSeriesSink{}
	completion, err := client.decode(context.Background(), answer, validAttempt(t), sink)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return completion, sink.series
}

func wantRefused(t *testing.T, completion execution.ProviderCompletion, detail string) {
	t.Helper()
	if completion.Completeness != execution.CompletenessUnavailable || len(completion.RouteFacts.Attempts) != 1 ||
		completion.RouteFacts.Attempts[0].Detail != detail {
		t.Fatalf("completion = %+v / %+v, want UNAVAILABLE naming %s", completion.Completeness, completion.RouteFacts, detail)
	}
}

// One value past its bound is refused by name before the decoder holds it:
// a field nobody reads, a key, a series. What the decoder pulled - all it
// could have buffered - is the value's bound and the window's slack, not
// the value's length.
func TestAValuePastItsBoundIsRefusedBeforeItIsHeld(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxSeriesBytes = 64 << 10
	// A series of half the bound grows the decoder's buffer, and the read
	// that ends it fills the buffer from the next one - the two arrive in one
	// read, as a body does over the network: the next window is measured from
	// where the decoder consumed to, not from what it pulled.
	opening := `{"series":[`
	half := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["` +
		strings.Repeat("x", 32<<10) + `"],"values":[[1700123456789,12.5]]}`
	for _, test := range []struct {
		name   string
		answer *pulledReader
		detail string
		bound  int64
	}{
		{"a field nobody reads", answerOf(`{"padding":"`, int64(64<<20), `","series":[],"is_partial":false}`),
			"response=limit_response_bytes", otherValueBytes},
		{"a key", answerOf(`{"`, int64(64<<20), `":1,"series":[],"is_partial":false}`),
			"response=limit_response_bytes", otherValueBytes},
		{"a series", answerOf(`{"series":[{"name":"`, int64(64<<20), `"}],"is_partial":false}`),
			"response=limit_series_bytes", 64 << 10},
		{"a series after one the decoder read ahead of", answerOf(opening+half+`,{"name":"`+strings.Repeat("x", 64<<10), int64(64<<20), `"}],"is_partial":false}`),
			"response=limit_series_bytes", int64(len(opening)+len(half)) + 64<<10},
		// Met where the decoder's error is replaced with words of its own
		// ("series must be an array"): still the limit it is.
		{"series that are no array", answerOf(`{"series":"`, int64(64<<20), `","is_partial":false}`),
			"response=limit_response_bytes", otherValueBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			completion, _ := decodeWindowed(t, limits, test.answer)
			wantRefused(t, completion, test.detail)
			if most := test.bound + valueWindowSlack + 64; test.answer.pulled > most {
				t.Fatalf("the decoder pulled %d bytes of a 64 MiB value, want at most its bound and the slack, %d", test.answer.pulled, most)
			}
		})
	}
}

// Series of exactly the per-series bound, back to back, are each within
// it: the window of each is measured from where the decoder consumed to,
// not from what it had already read ahead.
func TestSeriesOfExactlyTheBoundBackToBackAreAllDelivered(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["`
	tail := `"],"values":[[1700123456789,12.5]]}`
	const count = 200
	var body strings.Builder
	body.WriteString(`{"series":[`)
	for index := 0; index < count; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		body.WriteString(series + strings.Repeat("x", 4000) + tail)
	}
	body.WriteString(`],"is_partial":false}`)
	limits := DefaultLimits()
	limits.MaxSeriesBytes = int64(len(series) + 4000 + len(tail))
	completion, delivered := decodeWindowed(t, limits, answerOf(body.String()))
	if completion.Completeness != execution.CompletenessFull || delivered != count {
		t.Fatalf("completion = %+v / %+v, %d delivered, want every series of exactly the bound delivered", completion.Completeness, completion.RouteFacts, delivered)
	}
}

// There is no bound on the whole answer: one longer than the client's
// default body bound once was (64 MiB), every value within its own, is
// delivered whole.
func TestAnAnswerLongerThanAnyBodyBoundIsDeliveredWhole(t *testing.T) {
	series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["`
	tail := `"],"values":[[1700123456789,12.5]]}`
	value := strings.Repeat("y", 7000)
	const count = 10_000
	parts := []any{`{"series":[`}
	for index := 0; index < count; index++ {
		separator := ","
		if index == 0 {
			separator = ""
		}
		parts = append(parts, separator+series+value+tail)
	}
	parts = append(parts, `],"is_partial":false}`)
	answer := answerOf(parts...)
	limits := DefaultLimits()
	limits.MaxSeries = count
	completion, delivered := decodeWindowed(t, limits, answer)
	if completion.Completeness != execution.CompletenessFull || delivered != count {
		t.Fatalf("completion = %+v / %+v, %d delivered, want the whole answer delivered", completion.Completeness, completion.RouteFacts, delivered)
	}
	if answer.pulled <= 64<<20 {
		t.Fatalf("fixture: the answer is %d bytes, want it past 64 MiB", answer.pulled)
	}
}

// A named output's answer is bounded by the Slot's share however many
// in-bound series it carries: each is reserved as it is read, so an answer
// past the share is refused while it is read, holding no more than the
// share; so is the answer of an output nobody asked for.
func TestANamedAnswerPastTheShareIsRefusedWhileItIsRead(t *testing.T) {
	series := func(count int) string {
		each := make([]string, 0, count)
		for index := 0; index < count; index++ {
			each = append(each, namedSeries(hostOne, ipOne, `[[1700123060000,1]]`))
		}
		return strings.Join(each, ",")
	}
	for _, test := range []struct {
		name   string
		answer string
	}{
		{"an asked output", namedAnswer(namedOutput("A", "SUCCESS", false, "", series(4000)))},
		{"an output nobody asked for, named after its series", namedAnswer(
			`{"state":"SUCCESS","series":[`+series(4000)+`],"reference_name":"Z","is_partial":false}`,
			namedOutput("A", "SUCCESS", false, "", series(1)))},
	} {
		t.Run(test.name, func(t *testing.T) {
			reserver := &testReserver{limit: 64 << 10}
			completion := decodeReserving(t, test.answer, reserver)
			if completion.Completeness != execution.CompletenessUnavailable || completion.NamedOutputs != nil || reserver.refused == 0 || reserver.held != 0 {
				t.Fatalf("held %d (peak %d) refused %d completion %+v / %+v, want the answer refused while read and nothing left held",
					reserver.held, reserver.peak, reserver.refused, completion.Completeness, completion.RouteFacts)
			}
			if reserver.peak > reserver.limit {
				t.Fatalf("held %d at the peak, past the share %d", reserver.peak, reserver.limit)
			}
		})
	}
	// Named before its series, an output nobody asked for is read past and
	// none of it is held.
	reserver := &testReserver{limit: 64 << 10}
	completion := decodeReserving(t, namedAnswer(namedOutput("Z", "SUCCESS", false, "", series(4000)),
		namedOutput("A", "SUCCESS", false, "", series(1)), namedOutput("B", "SUCCESS", false, "", series(1)),
		namedOutput("C", "SUCCESS", false, "", series(1))), reserver)
	if reserver.refused != 0 || completion.NamedOutputs == nil {
		t.Fatalf("refused %d completion %+v / %+v, want an output nobody asked for read past without holding it",
			reserver.refused, completion.Completeness, completion.RouteFacts)
	}
}
