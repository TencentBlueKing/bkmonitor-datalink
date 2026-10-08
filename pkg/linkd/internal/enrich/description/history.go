package description

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// HistoryEvidence 保存检测时的比较事实；Value 是算法实际用于模板的历史值。
// Direction 为首个命中分支 floor/ceil；高级算法的 Interval、FetchType 来自该分支。
// Ratio/Shock 采用 bk-monitor Serializer 的 float 语义。
type HistoryEvidence struct {
	Value     Number
	Direction string
	Percent   float64
	Interval  int64
	FetchType string
	Ratio     float64
	Shock     float64
}

func renderHistory(u unitDefinition, a Algorithm) (string, bool, error) {
	if a.DetectionMatched == nil || a.History == nil {
		return "", false, invalid("detection_evidence_missing")
	}
	if !*a.DetectionMatched {
		return "", false, nil
	}
	h := a.History
	if !h.Value.valid {
		return "", false, invalid("history_value_missing")
	}
	value, err := u.format(h.Value)
	if err != nil {
		return "", false, err
	}
	if a.Type == "RingRatioAmplitude" {
		if !finite(h.Ratio) || !finite(h.Shock) {
			return "", false, invalid("history_config_invalid")
		}
		return " - 前一时刻值" + value + "的绝对值 >= 前一时刻值" + value + " * " + pythonNumber(h.Ratio, false) + " + " + pythonNumber(h.Shock, false) + u.thresholdSuffix(a.UnitPrefix), true, nil
	}
	if !finite(h.Percent) || h.Percent <= 0 || (h.Direction != "floor" && h.Direction != "ceil") {
		return "", false, invalid("history_config_invalid")
	}
	verb := "下降"
	if h.Direction == "ceil" {
		verb = "上升"
	}
	var comparison string
	switch a.Type {
	case "SimpleRingRatio":
		comparison = "较前一时刻"
	case "SimpleYearRound":
		comparison = "较上周同一时刻"
	case "AdvancedRingRatio", "AdvancedYearRound":
		if h.Interval < 1 || h.Interval > 1_000_000 || (h.FetchType != "avg" && h.FetchType != "last") {
			return "", false, invalid("history_config_invalid")
		}
		fetch := "均值"
		if h.FetchType == "last" {
			fetch = "瞬间值"
		}
		if a.Type == "AdvancedRingRatio" {
			comparison = fmt.Sprintf("较前%d个时间点的%s", h.Interval, fetch)
		} else {
			comparison = fmt.Sprintf("较前%d天内同一时刻绝对值的%s", h.Interval, fetch)
		}
	}
	return comparison + "(" + value + ")" + verb + "超过" + pythonNumber(h.Percent, false) + "%", true, nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// NoDataEvidence 是无数据检测时的两个周期计数，不是配置的 continuous。
type NoDataEvidence struct {
	NoDataPeriods  int64
	AnomalyPeriods int64
}

func renderNoData(f Facts) (string, error) {
	h := f.NoData
	if len(f.Algorithms) != 0 || len(f.ItemName) > 4096 || !utf8.ValidString(f.ItemName) || h.NoDataPeriods < 0 || h.AnomalyPeriods < 0 || h.NoDataPeriods > 1_000_000 || h.AnomalyPeriods > 1_000_000 {
		return "", invalid("nodata_evidence_invalid")
	}
	periods := h.NoDataPeriods
	if periods == 0 {
		periods = h.AnomalyPeriods
	}
	if periods == 0 {
		return "", invalid("not_triggered")
	}
	content := fmt.Sprintf("当前指标(%s)已经有%d个周期无数据上报", f.ItemName, periods)
	if h.NoDataPeriods > h.AnomalyPeriods {
		content += fmt.Sprintf("，并且数据上报延时%d个周期", h.NoDataPeriods-h.AnomalyPeriods)
	}
	return content, nil
}

// ForecastEvidence 保存检测选中的预测点，不用当前预测结果重算文案。
type ForecastEvidence struct {
	Value        Number
	AfterSeconds int64
	BoundType    string
}

func renderForecast(itemName string, u unitDefinition, a Algorithm) (string, bool, error) {
	if a.DetectionMatched == nil || a.Forecast == nil {
		return "", false, invalid("detection_evidence_missing")
	}
	if !*a.DetectionMatched {
		return "", false, nil
	}
	f := a.Forecast
	if !f.Value.valid || f.AfterSeconds < 0 || f.AfterSeconds > 366*86400 {
		return "", false, invalid("forecast_evidence_invalid")
	}
	bound := "预测值"
	switch f.BoundType {
	case "upper":
		bound = "预测上界"
	case "lower":
		bound = "预测下界"
	case "middle":
	default:
		return "", false, invalid("forecast_evidence_invalid")
	}
	conditions, matched, err := renderThreshold(f.Value, u, a)
	if err != nil || !matched {
		return "", false, err
	}
	// 预测模板的条件无前导空格，组内用“ 且 ”；指标和条件之间只有一个空格。
	conditions = strings.TrimPrefix(conditions, " ")
	conditions = strings.ReplaceAll(conditions, "且 ", " 且 ")
	value, err := u.format(f.Value)
	if err != nil {
		return "", false, err
	}
	return itemName + " " + conditions + ", 将于" + hms(f.AfterSeconds) + "后满足条件, " + bound + value, true, nil
}

// 与 bk-monitor hms_string 默认 display_num=2 一致，只展示前两个非零单位。
func hms(seconds int64) string {
	var parts []string
	for i, scale := range []int64{86400, 3600, 60, 1} {
		value := seconds / scale
		seconds %= scale
		if value > 0 {
			parts = append(parts, strconv.FormatInt(value, 10)+[]string{"d", "h", "m", "s"}[i])
		}
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}
