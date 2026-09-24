package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestSharedTopologyHandlerValidationCases(t *testing.T) {
	log.InitTestLogger()
	tests := []struct {
		name          string
		rangeQuery    bool
		body          string
		status        int
		dataCodes     []int
		requestResult string
	}{
		{name: "instant 空批次", body: `{"query_list":[]}`, status: http.StatusOK, dataCodes: []int{}, requestResult: "empty"},
		{name: "range 空批次", rangeQuery: true, body: `{"query_list":[]}`, status: http.StatusOK, dataCodes: []int{}, requestResult: "empty"},
		{name: "instant 缺少时间点", body: `{"query_list":[{"source_type":"node"}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest}, requestResult: "failed"},
		{name: "range 缺少时间范围", rangeQuery: true, body: `{"query_list":[{"source_type":"node","step":"1m"}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest}, requestResult: "failed"},
		{name: "instant 错用时间范围", body: `{"query_list":[{"timestamp":1700000000,"start_time":1700000000,"end_time":1700000060}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest}, requestResult: "failed"},
		{name: "range 错用时间点", rangeQuery: true, body: `{"query_list":[{"timestamp":1700000000}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest}, requestResult: "failed"},
		{name: "HTTP 和模型拒绝不重复计数", body: `{"query_list":[{}, {"timestamp":1700000000,"source_type":"node"}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest, http.StatusBadRequest}, requestResult: "failed"},
		{name: "非法 JSON", body: `{`, status: http.StatusBadRequest, requestResult: "rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := "instant"
			if tt.rangeQuery {
				mode = "range"
			}
			requestBefore := topologyOperationCounter(t, "request", mode, tt.requestResult)
			queriesBefore := topologyOperationCounter(t, "query", mode, "rejected")
			response := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(response)
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tt.body))
			if tt.rangeQuery {
				HandlerAPIRelationV1Beta3TopologyRange(ginContext)
			} else {
				HandlerAPIRelationV1Beta3Topology(ginContext)
			}
			require.Equal(t, tt.status, response.Code)
			require.Equal(t, requestBefore+1, topologyOperationCounter(t, "request", mode, tt.requestResult))
			require.Equal(t, queriesBefore+float64(len(tt.dataCodes)), topologyOperationCounter(t, "query", mode, "rejected"))
			if tt.status != http.StatusOK {
				return
			}

			var result cmdb.SharedTopologyResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			require.Len(t, result.Data, len(tt.dataCodes))
			for index, code := range tt.dataCodes {
				require.Equal(t, code, result.Data[index].Code)
				require.NotNil(t, result.Data[index].Snapshots)
			}
		})
	}
}

func topologyOperationCounter(t *testing.T, scope, mode, result string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_cmdb_topology_operations_total" {
			continue
		}
		for _, sample := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range sample.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["scope"] == scope && labels["query_mode"] == mode && labels["result"] == result {
				return sample.GetCounter().GetValue()
			}
		}
	}
	return 0
}
