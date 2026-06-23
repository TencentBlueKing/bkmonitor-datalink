package aegisv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWhitelistResponse_TimeFieldsAreNumbers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, routeAegisV2Whitelist, nil)
	resp := httptest.NewRecorder()

	httpSvc.Whitelist(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Code            int                `json:"code"`
		Msg             string             `json:"msg"`
		IsInWhiteList   int                `json:"is_in_white_list"`
		SampleMap       whitelistSampleMap `json:"sample_map"`
		ServerTime      int64              `json:"server_time"`
		StartServerTime int64              `json:"start_server_time"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 0, body.Code)
	assert.Equal(t, "success", body.Msg)
	assert.Equal(t, 0, body.IsInWhiteList)
	assert.Equal(t, whitelistSample, body.SampleMap)
	assert.Greater(t, body.ServerTime, int64(0))
	assert.GreaterOrEqual(t, body.ServerTime, body.StartServerTime)
	assert.Equal(t, body.ServerTime, body.StartServerTime)
}

func TestWhitelistResponse_StartServerTimeUsesUIDTimestamp(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodGet,
		routeAegisV2Whitelist+"?uid=user_1781749136169_b838746b&topic=SDK-daffdasfdasfdsafdas",
		nil,
	)
	resp := httptest.NewRecorder()

	httpSvc.Whitelist(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		ServerTime      int64 `json:"server_time"`
		StartServerTime int64 `json:"start_server_time"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.EqualValues(t, 1781749136169, body.StartServerTime)
	assert.GreaterOrEqual(t, body.ServerTime, body.StartServerTime)
}
