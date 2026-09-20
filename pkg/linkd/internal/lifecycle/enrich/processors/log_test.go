package processors

import (
	"context"
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestLogInvalidRelatedInfoReturnsPartial(t *testing.T) {
	t.Parallel()
	reader := &logTestReader{strategy: logStrategy(models.CWMonitorItemTypeLogKeyword, "error"), sourceName: "日志源"}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{})
	alert.ExtraData = domain.JSONObject{"log_related_info": json.RawMessage(`"text"`)}
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Log{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	envelope := payload.Processors[3][rules.LogProcessor]
	if result.Status != domain.EnrichStatusPartial || envelope.Status != domain.EnrichStatusPartial || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != enrich.DiagnosticCodeInvalidField {
		t.Fatalf("status=%q log=%#v", result.Status, envelope)
	}
}

func TestLogThemeReaderOverridesAndValidatesStrategyFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		theme      models.LogTheme
		found      bool
		wantName   string
		wantStatus domain.EnrichStatus
		wantDep    string
	}{
		{name: "tenant theme", theme: models.LogTheme{TenantID: "tenant-a", ID: 2, Name: "真实主题"}, found: true, wantName: "真实主题", wantStatus: domain.EnrichStatusSucceeded},
		{name: "tenant mismatch", theme: models.LogTheme{TenantID: "tenant-b", ID: 2, Name: "其他主题"}, found: true, wantName: "日志主题", wantStatus: domain.EnrichStatusSucceeded, wantDep: rules.DependencyLogTheme},
		{name: "missing", found: false, wantName: "日志主题", wantStatus: domain.EnrichStatusSucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &logTestReader{strategy: logStrategy(models.CWMonitorItemTypeLog, "error"), sourceName: "日志源", theme: tc.theme, themeFound: tc.found}
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Log{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader, LogTheme: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			var values models.LogValues
			if err := json.Unmarshal(mustRawObject(t, payload.Processors[3][rules.LogProcessor].Value), &values); err != nil {
				t.Fatal(err)
			}
			if values.LogThemeName != tc.wantName {
				t.Fatalf("name=%q", values.LogThemeName)
			}
			logEnvelope := payload.Processors[3][rules.LogProcessor]
			if tc.wantDep != "" && (len(logEnvelope.Diagnostics) == 0 || logEnvelope.Diagnostics[0].Dependency != tc.wantDep) {
				t.Fatalf("diagnostics=%#v", logEnvelope.Diagnostics)
			}
		})
	}
}

func TestLogContentProjection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		classification       rules.DisplayClassification
		content, query, want string
	}{
		{classification: rules.DisplayLogMetric, content: "count >= 3,关联信息：host=web", want: "count >= 3"},
		{classification: rules.DisplayLogKeyword, content: "keyword(error) >= 2, 当前值 3,关联信息：host=web", query: "error", want: "匹配到【error】关键字次数 >= 2, 当前值 3"},
		{classification: rules.DisplayLogKeyword, content: "keyword(error)已经5个周期无数据,关联信息：host=web", query: "error", want: "【error】关键字已经5个周期无数据"},
	} {
		if got := logDisplayContent(tc.content, tc.classification, tc.query); got != tc.want {
			t.Fatalf("content=%q want=%q", got, tc.want)
		}
	}
}

func TestLogMetricFixtureProjection(t *testing.T) {
	t.Parallel()
	reader := &logTestReader{strategy: logStrategy(models.CWMonitorItemTypeLog, ""), sourceName: "基础监控"}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{})
	alert.Content = "AVG(服务器发送字节数) >= 2.0, 当前值1189.352608,关联信息：host=web"
	alert.SubjectName = "寒江孤影"
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Log{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	logEnvelope := payload.Processors[3][rules.LogProcessor]
	var log models.LogValues
	if err := json.Unmarshal(mustRawObject(t, logEnvelope.Value), &log); err != nil {
		t.Fatal(err)
	}
	if log.LogThemeID != float64(2) || log.LogThemeName != "日志主题" || len(log.CWLabels) != 4 {
		t.Fatalf("log=%#v", log)
	}
	metric := payload.Processors[4][rules.MetricProcessor]
	var metricValues models.MetricValues
	if err := json.Unmarshal(mustRawObject(t, metric.Value), &metricValues); err != nil {
		t.Fatal(err)
	}
	if metricValues.MetricName != "--" || metricValues.ResultTableID != "" {
		t.Fatalf("metric=%#v", metricValues)
	}
}

func TestLogMetricAndKeywordProjection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		itemType  models.CWMonitorItemType
		query     string
		expected  string
		related   string
		wantTitle string
	}{
		{name: "metric", itemType: models.CWMonitorItemTypeLog, query: "level:error", expected: "level:error", wantTitle: "日志主题发生了level:error告警"},
		{name: "keyword", itemType: models.CWMonitorItemTypeLogKeyword, query: "error", expected: "error", related: `{"host":"web-1"}`, wantTitle: "日志主题发生了【error】关键字告警"},
		{name: "keyword fallback", itemType: models.CWMonitorItemTypeLogKeyword, expected: "--", wantTitle: "日志主题发生了【】关键字告警"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &logTestReader{strategy: logStrategy(tc.itemType, tc.query), sourceName: "日志源"}
			alert := processorBaseTargetAlert(t, domain.DimensionMap{})
			alert.SubjectName = "日志主题"
			alert.ExtraData = domain.JSONObject{}
			if tc.related != "" {
				alert.ExtraData["log_related_info"] = json.RawMessage(tc.related)
			}
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Log{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			logValue := payload.Processors[3][rules.LogProcessor]
			var log models.LogValues
			if err := json.Unmarshal(mustRawObject(t, logValue.Value), &log); err != nil {
				t.Fatal(err)
			}
			if log.LogQueryString != tc.expected || log.LogThemeName != "日志主题" || len(log.CWLabels) != 4 {
				t.Fatalf("log=%#v", log)
			}
			display := payload.Processors[2][rules.DisplayProcessor]
			var title string
			if err := json.Unmarshal(display.Value["title"], &title); err != nil {
				t.Fatal(err)
			}
			if title != tc.wantTitle {
				t.Fatalf("title=%q", title)
			}
		})
	}
}

func logStrategy(itemType models.CWMonitorItemType, query string) models.CWStrategy {
	biz := int64(2)
	metricField, aggregateMethod, dataType, dataSource := "responseBytes", "AVG", "time_series", "bk_log_search"
	if itemType == models.CWMonitorItemTypeLogKeyword {
		metricField, aggregateMethod, dataType = "*", "COUNT", "log"
	}
	return models.CWStrategy{BKBizID: &biz, Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, Name: "日志主题", MonitorItemType: itemType, MetricSource: models.CWMetricSourceKLC, SourceConfig: domain.JSONObject{"log_theme_id": json.RawMessage(`2`), "log_theme_name": json.RawMessage(`"日志主题"`), "query_string": json.RawMessage(`"` + query + `"`)}, StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{MetricField: metricField, AggregateMethod: aggregateMethod, DataSourceLabel: dataSource, DataTypeLabel: dataType, QueryString: query, IndexSetID: 5, AggregatePeriod: 60, AggregateBy: []string{"bk_biz_id"}}}}}}
}

type logTestReader struct {
	strategy   models.CWStrategy
	sourceName string
	theme      models.LogTheme
	themeFound bool
}

func (r *logTestReader) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	return r.strategy, true, nil
}
func (*logTestReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}
func (*logTestReader) GetModelByCode(context.Context, string, string) (enrich.Model, bool, error) {
	return enrich.Model{}, false, nil
}
func (*logTestReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return enrich.Instance{}, false, nil
}
func (r *logTestReader) GetLogTheme(context.Context, string, int64) (models.LogTheme, bool, error) {
	return r.theme, r.themeFound, nil
}
func (r *logTestReader) GetAlarmSourceName(context.Context, string, string) (string, bool, error) {
	return r.sourceName, true, nil
}
