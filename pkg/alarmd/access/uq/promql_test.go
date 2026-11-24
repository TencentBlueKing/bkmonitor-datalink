// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rangeClient(t *testing.T, handler http.HandlerFunc) (*Client, *http.Request) {
	t.Helper()
	var seen *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seen = request.Clone(request.Context())
		handler(response, request)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, seen
}

func rangeRequest() RangeRequest {
	end := time.Unix(1789006380, 0)
	return RangeRequest{
		PromQL: "max(alarmd_fleet_objects)", SpaceUID: "bkcc__2",
		Start: end.Add(-time.Hour), End: end, Step: time.Minute,
	}
}

func TestRangeReadsTheSamples(t *testing.T) {
	client, _ := rangeClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"series":[{"columns":["_time","_value"],"types":["float","float"],
			"values":[[1789006260000,931],[1789006320000,930]]}],"is_partial":false}`))
	})
	result, err := client.Range(context.Background(), rangeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Unavailable() {
		t.Fatalf("provider reported unavailable: %+v", result)
	}
	if len(result.Points) != 2 || result.Points[0].Value != 931 || result.Points[0].AtUnixMilli != 1789006260000 {
		t.Fatalf("points = %+v", result.Points)
	}
}

// The provider answers a missing space or a missing metric with HTTP 200 and an
// empty series list, putting the reason in the body. Reading only the status
// line makes a misconfigured scope look exactly like a quiet system, which is
// the worse of the two to get wrong because it reads as good news.
func TestRangeDoesNotReadAProviderRefusalAsSilence(t *testing.T) {
	for _, code := range []string{"SPACE_IS_NOT_EXISTS", "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"} {
		client, _ := rangeClient(t, func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write([]byte(`{"series":[],"status":{"code":"` + code + `","message":"detail"}}`))
		})
		result, err := client.Range(context.Background(), rangeRequest())
		if err != nil {
			t.Fatal(err)
		}
		if !result.Unavailable() || result.Code != code {
			t.Fatalf("%s was read as an empty window: %+v", code, result)
		}
	}
}

// An empty window with no code is a real "nothing happened", and must not be
// dressed up as a failure either.
func TestRangeKeepsAnEmptyWindowEmpty(t *testing.T) {
	client, _ := rangeClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"series":[],"is_partial":false}`))
	})
	result, err := client.Range(context.Background(), rangeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Unavailable() || len(result.Points) != 0 {
		t.Fatalf("empty window = %+v", result)
	}
}

// Without the space scope the provider answers 200 and nothing else; refusing
// the request here turns a silent misconfiguration into a stated one.
func TestRangeRefusesAQueryWithoutScope(t *testing.T) {
	client, _ := rangeClient(t, func(response http.ResponseWriter, _ *http.Request) {
		t.Error("a scopeless query reached the provider")
	})
	request := rangeRequest()
	request.SpaceUID = ""
	if _, err := client.Range(context.Background(), request); err == nil {
		t.Fatal("a query without a space scope was accepted")
	}
}

func TestRangeSendsTheScopeAndSourceHeaders(t *testing.T) {
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		headers = request.Header.Clone()
		if request.URL.Path != "/query/ts/promql" {
			t.Errorf("path = %s", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{"series":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Range(context.Background(), rangeRequest()); err != nil {
		t.Fatal(err)
	}
	// Measured against the deployment: only the space header makes the provider
	// resolve the scope; bk_biz_ids in the body does not.
	if headers.Get(headerSpace) != "bkcc__2" || headers.Get(headerQuerySource) != "alarmd" {
		t.Fatalf("headers = %v", headers)
	}
}

// Each series keeps its group labels and its own samples, so a chart of
// several lines -- one per stage, one per pod -- can tell them apart.
func TestRangeKeepsEachSeriesWithItsLabels(t *testing.T) {
	client, _ := rangeClient(t, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"series":[
			{"group_keys":["pod"],"group_values":["alarmd-a"],"columns":["_time","_value"],"values":[[1789006260000,0.04]]},
			{"group_keys":["pod"],"group_values":["alarmd-b"],"columns":["_time","_value"],"values":[[1789006260000,0.05],[1789006320000,0.06]]}]}`))
	})
	result, err := client.Range(context.Background(), rangeRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 2 || result.Series[0].Labels["pod"] != "alarmd-a" || len(result.Series[0].Points) != 1 ||
		result.Series[1].Labels["pod"] != "alarmd-b" || len(result.Series[1].Points) != 2 || result.Series[1].Points[1].Value != 0.06 {
		t.Fatalf("series = %+v, want two series with their pods and samples", result.Series)
	}
}

// A range read that failed with no answer, or with an answer it could not
// read, names the trace id it was sent with, as a refusal does.
func TestARangeFailureNamesTheTraceItWasSentWith(t *testing.T) {
	var sent string
	client, _ := rangeClient(t, func(response http.ResponseWriter, request *http.Request) {
		sent = request.Header.Get(headerTraceParent)
		_, _ = response.Write([]byte(`{"series":[`))
	})
	_, err := client.Range(context.Background(), rangeRequest())
	parts := strings.Split(sent, "-")
	if err == nil || len(parts) != 4 || !strings.Contains(err.Error(), "trace "+parts[1]) {
		t.Fatalf("error %v, want the sent trace id of %q", err, sent)
	}
}

// widestTrendAnswer is the largest answer a page asks a range for, as
// rangeMaxBodyBytes is sized from: a line grouped by pod over the longest
// window at its step (24h at 10m, 145 points), 400 pod names in a day.
func widestTrendAnswer(pods int) string {
	var body strings.Builder
	body.WriteString(`{"series":[`)
	for pod := 0; pod < pods; pod++ {
		if pod > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"columns":["_time","_value"],"types":["float","float"],"group_keys":["pod"],"group_values":["bk-monitor-alarmd-worker-7d9f8c6b5-%05d"],"values":[`, pod)
		for point := 0; point < 145; point++ {
			if point > 0 {
				body.WriteString(",")
			}
			fmt.Fprintf(&body, "[%d,%.6f]", 1789006380000-int64(144-point)*600_000, 1234567890.123456+float64(point))
		}
		body.WriteString("]}")
	}
	body.WriteString(`],"is_partial":false}`)
	return body.String()
}

// A range answer is read whole, so it has a bound of its own: the widest a
// page asks for is read under it with room to spare (the bound is four times
// it), and one past it is refused by name rather than cut short into a
// decode error.
func TestTheWidestTrendAnswerIsReadWhole(t *testing.T) {
	widest := widestTrendAnswer(400)
	if got := len(widest); got > rangeMaxBodyBytes/4 {
		t.Fatalf("the widest trend answer is %d bytes, past a quarter of the bound %d: the bound no longer has its margin", got, rangeMaxBodyBytes)
	}
	client, _ := rangeClient(t, func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(widest)) })
	result, err := client.Range(context.Background(), rangeRequest())
	if err != nil || len(result.Series) != 400 || len(result.Series[399].Points) != 145 {
		t.Fatalf("Range() = %d series, err %v, want the widest answer read whole", len(result.Series), err)
	}
	past := widestTrendAnswer(400 * (rangeMaxBodyBytes/len(widest) + 1))
	client, _ = rangeClient(t, func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(past)) })
	if _, err := client.Range(context.Background(), rangeRequest()); !errors.Is(err, ErrResponseBytesExceeded) {
		t.Fatalf("Range() over the bound = %v, want it refused as %v", err, ErrResponseBytesExceeded)
	}
}
