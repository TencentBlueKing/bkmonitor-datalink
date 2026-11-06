package elasticsearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClearScrollRequiresExplicitIDs(testContext *testing.T) {
	instance := &Instance{}
	if err := instance.ClearScroll(context.Background()); err != nil {
		testContext.Fatal(err)
	}
	for _, scrollIDs := range [][]string{{""}, {"_all"}, {"session-id", "_all"}, {"session-id", ""}} {
		err := instance.ClearScroll(context.Background(), scrollIDs...)
		if err == nil || err.Error() != "scroll cleanup requires explicit non-empty scroll IDs" {
			testContext.Fatalf("unsafe scroll IDs were not rejected before connecting: %q, error: %v", scrollIDs, err)
		}
	}
}

func TestClearScrollResponses(t *testing.T) {
	// Legacy tests leave the global mock transport active; these cases verify real HTTP requests.
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	testCases := []struct {
		name      string
		status    int
		body      string
		username  string
		password  string
		wantError bool
	}{
		{name: "without_authentication", status: http.StatusOK, body: `{"succeeded":true,"num_freed":2}`},
		{name: "with_basic_authentication", status: http.StatusOK, body: `{"succeeded":true,"num_freed":2}`, username: "test-user", password: "test-password"},
		{name: "already_cleared", status: http.StatusNotFound, body: `{"succeeded":true,"num_freed":0}`},
		{name: "zero_freed", status: http.StatusOK, body: `{"succeeded":true,"num_freed":0}`},
		{name: "node_failure_with_zero_freed", status: http.StatusNotFound, body: `{"succeeded":false,"num_freed":0}`, wantError: true},
		{name: "failure_with_zero_freed", status: http.StatusOK, body: `{"succeeded":false,"num_freed":0}`, wantError: true},
		{name: "partial_failure", status: http.StatusOK, body: `{"succeeded":false,"num_freed":1}`, wantError: true},
		{name: "gateway_not_found", status: http.StatusNotFound, body: `{"error":"route not found"}`, wantError: true},
		{name: "malformed_not_found", status: http.StatusNotFound, body: `<html>not found</html>`, wantError: true},
		{name: "malformed_success", status: http.StatusOK, body: `{`, wantError: true},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":{"type":"security_exception","reason":"test denial"},"status":401}`, wantError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodDelete, request.Method)
				assert.Equal(t, "/gateway/_search/scroll/", request.URL.Path)
				assert.Equal(t, "test-token", request.Header.Get("X-Test-Token"))
				username, password, authenticated := request.BasicAuth()
				assert.Equal(t, testCase.username != "", authenticated)
				assert.Equal(t, testCase.username, username)
				assert.Equal(t, testCase.password, password)
				body, err := io.ReadAll(request.Body)
				assert.NoError(t, err)
				assert.JSONEq(t, `{"scroll_id":["first-scroll-id","second-scroll-id"]}`, string(body))
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(testCase.status)
				_, _ = fmt.Fprint(response, testCase.body)
			}))
			t.Cleanup(server.Close)
			instance, err := NewInstance(context.Background(), &InstanceOption{
				Connect: Connect{Address: server.URL + "/gateway", UserName: testCase.username, Password: testCase.password},
				Headers: map[string]string{"X-Test-Token": "test-token"},
				Timeout: time.Second,
			})
			require.NoError(t, err)
			err = instance.ClearScroll(context.Background(), "first-scroll-id", "second-scroll-id")
			if testCase.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.EqualValues(t, 1, requests.Load(), "cleanup must not retry")
		})
	}
}
