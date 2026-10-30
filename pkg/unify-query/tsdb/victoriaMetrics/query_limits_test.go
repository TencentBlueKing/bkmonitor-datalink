package victoriaMetrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestUnlimitedVMUsesHTTPWithoutInternalOrInstantTimeout(t *testing.T) {
	mock.Init()
	oldTransport := http.DefaultTransport
	transport := &http.Transport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = oldTransport })
	bodies := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var outer map[string]string
		if err := json.NewDecoder(r.Body).Decode(&outer); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var params struct {
			APIParams map[string]any `json:"api_params"`
		}
		if err := json.Unmarshal([]byte(outer["sql"]), &params); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		bodies <- params.APIParams
		_, _ = w.Write([]byte(`{"result":true,"code":"00","data":{"list":[{"status":"success","data":{"resultType":"vector","result":[]}}]}}`))
	}))
	defer server.Close()
	instance := &Instance{url: server.URL, timeout: time.Minute, curl: &curl.HttpCurl{}}
	for _, unlimited := range []bool{false, true} {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetExpand(ctx, &metadata.VmExpand{ResultTableList: []string{"test"}})
		if unlimited {
			ctx = promql.WithQueryLimitsDisabled(ctx)
			instance.timeout = 0
		}
		_, _, err := instance.DirectQueryWithPartial(ctx, "up", time.Unix(1700000000, 0))
		require.NoError(t, err)
		params := <-bodies
		if unlimited {
			require.NotContains(t, params, "timeout")
		} else {
			require.Equal(t, float64(60), params["timeout"])
		}
	}
	ctx := metadata.InitHashID(context.Background())
	metadata.SetExpand(ctx, &metadata.VmExpand{ResultTableList: []string{"test"}})
	_, _, err := instance.DirectQueryWithPartial(ctx, "up", time.Unix(1700000000, 0))
	require.Error(t, err)
	parent, cancel := context.WithCancel(promql.WithQueryLimitsDisabled(ctx))
	cancel()
	_, _, err = instance.DirectQueryWithPartial(parent, "up", time.Unix(1700000000, 0))
	require.Error(t, err)
	require.Empty(t, bodies)
}
