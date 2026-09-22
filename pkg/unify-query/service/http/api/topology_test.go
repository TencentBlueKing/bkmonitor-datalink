package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestSharedTopologyHandlerValidationCases(t *testing.T) {
	log.InitTestLogger()
	tests := []struct {
		name       string
		rangeQuery bool
		body       string
		status     int
		dataCodes  []int
	}{
		{name: "instant 空批次", body: `{"query_list":[]}`, status: http.StatusOK, dataCodes: []int{}},
		{name: "instant 缺少时间点", body: `{"query_list":[{"source_type":"node"}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest}},
		{name: "range 缺少时间范围", rangeQuery: true, body: `{"query_list":[{"source_type":"node","step":"1m"}]}`, status: http.StatusOK, dataCodes: []int{http.StatusBadRequest}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(response)
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tt.body))
			if tt.rangeQuery {
				HandlerAPIRelationV1Beta3TopologyRange(ginContext)
			} else {
				HandlerAPIRelationV1Beta3Topology(ginContext)
			}
			require.Equal(t, tt.status, response.Code)

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
