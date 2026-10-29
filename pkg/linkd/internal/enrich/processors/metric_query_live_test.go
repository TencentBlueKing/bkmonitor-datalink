package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/models"
)

// TestExternalMetricQueryLiveAlert 显式启用后，仅为外部来源在内存中
// 生成查询参数，再向 KMC 发送只读指标请求。连接与 Cookie 只从 stdin 读取，
// 不写文件、告警或消息；普通单测不依赖外部服务。
func TestExternalMetricQueryLiveAlert(t *testing.T) {
	if os.Getenv("LINKD_METRIC_QUERY_LIVE") != "1" {
		t.Skip("requires explicit test environment and transient stdin credentials")
	}
	var input struct {
		MySQL                                 datasources.MySQLConfig
		Event                                 domain.Event
		Alarm                                 map[string]any
		PublicationVersion                    int64
		MetricURL, Cookie, StartTime, EndTime string
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&input); err != nil {
		t.Fatal("invalid bounded live input")
	}
	if input.Event.EventSourceID == "built_in_bk" {
		t.Fatal("builtin query migration must be validated through the KAC metric endpoint")
	}
	if input.Cookie == "" {
		t.Fatal("transient authentication cookie is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	runtime, err := datasources.Open(ctx, datasources.Config{MySQL: &input.MySQL}, 2)
	if err != nil {
		t.Fatal("read-only datasource unavailable")
	}
	t.Cleanup(func() {
		if runtime.Close() != nil {
			t.Error("close datasource failed")
		}
	})

	// SplitRecord 只保留当前发布态。版本更新必须显式记录；仅调整内存中的
	// replay 输入来验证当前配置，不能冒充原历史配置或回写 Event。
	event := input.Event.Clone()
	oldVersion := event.Labels["strategy_version"]
	if input.PublicationVersion <= 0 {
		t.Fatal("current publication version required")
	}
	var version domain.Scalar
	if json.Unmarshal([]byte(strconv.FormatInt(input.PublicationVersion, 10)), &version) != nil {
		t.Fatal("invalid publication version")
	}
	event.Labels["strategy_version"] = version
	t.Logf("read-only current-publication replay: original_version=%v current_version=%d event=%s", oldVersion, input.PublicationVersion, event.EventID)
	scope, err := enrich.NewScope(event, runtime.Sources())
	if err != nil {
		t.Fatal("invalid live event")
	}
	result, err := (Metric{}).Process(ctx, scope)
	if err != nil || result.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("metric replay failed: status=%s diagnostics=%v", result.Status, result.Diagnostics)
	}
	var metric models.MetricValues
	if json.Unmarshal(mustRawObject(t, result.Value), &metric) != nil {
		t.Fatal("invalid metric result")
	}
	if metric.ResultTableID == "" || metric.MetricName == "" {
		t.Fatal("physical query location missing")
	}
	params := map[string]any{}
	encoded, err := json.Marshal(metric.MetricQueryParams)
	if err != nil || json.Unmarshal(encoded, &params) != nil {
		t.Fatal("invalid generated query parameters")
	}
	if params["expression"] != "" {
		t.Fatal("single native metric expression was not simplified")
	}
	queries, ok := params["query_configs"].([]any)
	if !ok || len(queries) != 1 || queries[0].(map[string]any)["table"] != metric.ResultTableID {
		t.Fatal("generated table references differ")
	}
	for key, value := range map[string]any{
		"model_id": input.Alarm["model_id"], "model_inst_id": input.Alarm["model_inst_id"], "Namespace": input.Alarm["Namespace"],
		"metric_name": metric.MetricName, "item": metric.DisplayName, "unit": metric.Unit,
		"aggregate_func": metric.AggregateFunc, "result_table_id": metric.ResultTableID, "where_condition": metric.WhereCondition,
		"start_time": input.StartTime, "end_time": input.EndTime,
	} {
		params[key] = value
	}
	encoded, err = json.Marshal(params)
	if err != nil {
		t.Fatal("cannot encode metric request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, input.MetricURL, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal("invalid metric URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", input.Cookie)
	req.Header.Set("Auth-APP", "BLUEKING_EYE")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("live metric request failed")
	}
	defer func() {
		if resp.Body.Close() != nil {
			t.Error("close metric response failed")
		}
	}()
	var response struct {
		Result  bool
		Code    any
		Message string
		Data    struct {
			Points []struct {
				Value *float64 `json:"value"`
			} `json:"points"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&response) != nil {
		t.Fatal("invalid bounded metric response")
	}
	nonNull := 0
	for _, point := range response.Data.Points {
		if point.Value != nil {
			nonNull++
		}
	}
	if resp.StatusCode != http.StatusOK || !response.Result || nonNull == 0 {
		t.Fatalf("query failed: HTTP=%d result=%t code=%v message=%s points=%d non_null=%d", resp.StatusCode, response.Result, response.Code, response.Message, len(response.Data.Points), nonNull)
	}
	t.Logf("generated query: table=%s metric=%s expression=empty; HTTP=%d code=%v points=%d non_null=%d", metric.ResultTableID, metric.MetricName, resp.StatusCode, response.Code, len(response.Data.Points), nonNull)
}
