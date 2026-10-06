package execution

import (
	"strings"
	"testing"
)

func TestRouteAttemptDetailHelpersStayBounded(t *testing.T) {
	for status, want := range map[int]string{503: "http_status=503", 404: "http_status=404", 0: "http_status=other", 1000: "http_status=other"} {
		if got := HTTPStatusRouteDetail(status); got != want {
			t.Fatalf("HTTPStatusRouteDetail(%d) = %q, want %q", status, got, want)
		}
	}
	for class, want := range map[string]string{
		TransportFailureTimeout: "transport=timeout", TransportFailureConnectionRefused: "transport=connection_refused",
		TransportFailureConnectionReset: "transport=connection_reset", TransportFailureDNS: "transport=dns",
		TransportFailureTLS: "transport=tls", TransportFailureEOF: "transport=eof",
		"": "transport=other", "free text with url https://x": "transport=other",
	} {
		if got := TransportRouteDetail(class); got != want {
			t.Fatalf("TransportRouteDetail(%q) = %q, want %q", class, got, want)
		}
	}
	if ResponseRouteDetail(ResponseFailureIsPartialMissing) != "response=is_partial_missing" || ResponseRouteDetail("x") != "response=other" {
		t.Fatal("ResponseRouteDetail is not bounded")
	}
	for detail, want := range map[string]string{
		"http_status=503": RouteDetailKindHTTPStatus, "transport=dns": RouteDetailKindTransport,
		"response=is_partial_missing": RouteDetailKindResponse, "": "", "body=whatever": "", "http_status": "",
	} {
		if got := RouteDetailKind(detail); got != want {
			t.Fatalf("RouteDetailKind(%q) = %q, want %q", detail, got, want)
		}
	}
}

// A deterministic UQ status code becomes a bounded, lower-cased response detail
// that fits the query failure detail grammar; anything outside the status code
// grammar collapses to status_other so the detail never carries body content.
func TestResponseStatusRouteDetailIsBounded(t *testing.T) {
	longest := "A" + strings.Repeat("B", 63)
	for code, want := range map[string]string{
		"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS": "response=status_space_table_id_field_is_not_exists",
		"QUERY_TS_STORAGE_TIMEOUT":           "response=status_query_ts_storage_timeout",
		"A1":                                 "response=status_a1",
		longest:                              "response=status_" + strings.ToLower(longest),
		"":                                   "response=status_other",
		"1A":                                 "response=status_other",
		"lower":                              "response=status_other",
		"HAS-DASH":                           "response=status_other",
		"https://user:secret@example.test/?token=secret": "response=status_other",
		strings.Repeat("A", 65):                          "response=status_other",
	} {
		got := ResponseStatusRouteDetail(code)
		if got != want {
			t.Fatalf("ResponseStatusRouteDetail(%q) = %q, want %q", code, got, want)
		}
		if RouteDetailKind(got) != RouteDetailKindResponse || len(got) > 96 {
			t.Fatalf("ResponseStatusRouteDetail(%q) = %q is not a bounded response detail", code, got)
		}
	}
}
