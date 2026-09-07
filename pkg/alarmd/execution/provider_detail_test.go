package execution

import "testing"

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
