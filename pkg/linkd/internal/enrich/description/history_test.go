package description

import (
	"strings"
	"testing"
)

func TestHistoricalDescriptionGoldenTemplates(t *testing.T) {
	t.Parallel()
	matched := true
	for _, tc := range []struct{ algorithm, direction, fetch, fragment string }{
		{"SimpleRingRatio", "floor", "", "较前一时刻(80.0%)下降超过20.0%"},
		{"SimpleRingRatio", "ceil", "", "较前一时刻(80.0%)上升超过20.0%"},
		{"SimpleYearRound", "floor", "", "较上周同一时刻(80.0%)下降超过20.0%"},
		{"SimpleYearRound", "ceil", "", "较上周同一时刻(80.0%)上升超过20.0%"},
		{"AdvancedRingRatio", "floor", "avg", "较前3个时间点的均值(80.0%)下降超过20.0%"},
		{"AdvancedRingRatio", "ceil", "last", "较前3个时间点的瞬间值(80.0%)上升超过20.0%"},
		{"AdvancedYearRound", "floor", "avg", "较前3天内同一时刻绝对值的均值(80.0%)下降超过20.0%"},
		{"AdvancedYearRound", "ceil", "last", "较前3天内同一时刻绝对值的瞬间值(80.0%)上升超过20.0%"},
		{"RingRatioAmplitude", "", "", " - 前一时刻值80.0%的绝对值 >= 前一时刻值80.0% * 1.5 + 2.0%"},
	} {
		t.Run(tc.algorithm+"/"+tc.direction, func(t *testing.T) {
			f := Facts{ItemName: "AVG(指标)", Unit: "percent", Value: number(t, "99"), Connector: "and", Algorithms: []Algorithm{{Type: tc.algorithm, UnitPrefix: "%", DetectionMatched: &matched, History: &HistoryEvidence{Value: number(t, "80.0"), Direction: tc.direction, Percent: 20, Interval: 3, FetchType: tc.fetch, Ratio: 1.5, Shock: 2}}}}
			got, err := Render(f)
			want := "AVG(指标)" + tc.fragment + ", 当前值99%"
			if err != nil || got != want {
				t.Fatalf("got %q err %v want %q", got, err, want)
			}
			f.Algorithms[0].History = nil
			if _, err := Render(f); err == nil {
				t.Fatal("invented history")
			}
		})
	}
}

func TestForecastGoldenDescriptions(t *testing.T) {
	t.Parallel()
	matched := true
	for _, bound := range []string{"middle", "upper", "lower"} {
		t.Run(bound, func(t *testing.T) {
			f := Facts{ItemName: "AVG(指标)", Unit: "percent", Value: number(t, "99"), Connector: "and", Algorithms: []Algorithm{{Type: "TimeSeriesForecasting", UnitPrefix: "%", Groups: [][]Condition{{{"gt", 100}, {"lt", 200}}}, DetectionMatched: &matched, Forecast: &ForecastEvidence{Value: number(t, "150.0"), AfterSeconds: 3661, BoundType: bound}}}}
			got, err := Render(f)
			label := map[string]string{"middle": "预测值", "upper": "预测上界", "lower": "预测下界"}[bound]
			want := "AVG(指标) > 100.0% 且 < 200.0%, 将于1h 1m后满足条件, " + label + "150.0%"
			if err != nil || got != want {
				t.Fatalf("got %q err %v want %q", got, err, want)
			}
			f.Algorithms[0].Forecast = nil
			if _, err := Render(f); err == nil {
				t.Fatal("invented forecast")
			}
		})
	}
}

func TestNoDataGoldenDescriptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		missing, anomaly int64
		want             string
	}{
		{5, 5, "当前指标(AVG(指标))已经有5个周期无数据上报"},
		{5, 3, "当前指标(AVG(指标))已经有5个周期无数据上报，并且数据上报延时2个周期"},
		{0, 3, "当前指标(AVG(指标))已经有3个周期无数据上报"},
	} {
		got, err := Render(Facts{ItemName: "AVG(指标)", NoData: &NoDataEvidence{NoDataPeriods: tc.missing, AnomalyPeriods: tc.anomaly}})
		if err != nil || got != tc.want {
			t.Fatalf("got %q err %v want %q", got, err, tc.want)
		}
	}
	for _, h := range []NoDataEvidence{{}, {NoDataPeriods: -1}, {AnomalyPeriods: 1_000_001}} {
		if text, err := Render(Facts{NoData: &h}); err == nil || text != "" {
			t.Fatal("accepted invalid no data evidence")
		}
	}
}

func TestHistoricalFactsValidation(t *testing.T) {
	matched := true
	f := Facts{Value: number(t, "99"), Connector: "and", Algorithms: []Algorithm{{Type: "AdvancedRingRatio", DetectionMatched: &matched, History: &HistoryEvidence{Value: number(t, "80"), Direction: "floor", Percent: 20, Interval: 0, FetchType: "avg"}}}}
	if _, err := Render(f); err == nil {
		t.Fatal("accepted zero interval")
	}
	f.Algorithms[0].History.Interval = 3
	f.Algorithms[0].History.FetchType = "future"
	if _, err := Render(f); err == nil {
		t.Fatal("accepted unknown fetch type")
	}
	f.Algorithms[0].History.FetchType = "avg"
	f.Algorithms[0].History.Direction = "both"
	if _, err := Render(f); err == nil {
		t.Fatal("accepted ambiguous branch")
	}
	f.Algorithms[0].History.Direction = "floor"
	matched = false
	if _, err := Render(f); err == nil || !strings.Contains(err.Error(), "not_triggered") {
		t.Fatal("rendered nonmatch")
	}
}
