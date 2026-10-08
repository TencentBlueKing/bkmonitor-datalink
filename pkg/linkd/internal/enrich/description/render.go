package description

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxContentBytes = 64 << 10
const maxAlgorithms = 32
const maxGroups = 64
const maxConditions = 64

// Error 表示不能生成确定内容的错误，Code 只包含低基数规则码。
// 错误不含原始消息、配置或检测值；调用方可据此阻断确定性失败。
type Error struct{ Code string }

func (e *Error) Error() string  { return "alert description: " + e.Code }
func invalid(code string) error { return &Error{Code: code} }

// PermanentContentFailure 标识缺事实、配置无效等确定性失败，供 Scheduler 保留队首。
func (e *Error) PermanentContentFailure() string { return e.Code }

// Condition 是阈值组内的一项条件。Threshold 采用 bk-monitor float 序列化语义。
type Condition struct {
	Method    string
	Threshold float64
}

// Algorithm 是一个已选级别的检测规则及专用事件事实。
// Threshold 的 Groups 为外层 OR、内层 AND；专用事件字段仅由对应算法读取。
type Algorithm struct {
	Type string
	// DetectionMatched 仅用于需要历史检测的专用规则；nil 表示未提供命中事实。
	DetectionMatched  *bool
	Groups            [][]Condition
	UnitPrefix        string
	ProcessName       string
	NonListeningPorts []int
	InaccurateListen  string
	BindIP            string
	// History 为检测时已经冻结的比较值及命中分支，不查询当前历史数据。
	History  *HistoryEvidence
	Forecast *ForecastEvidence
}

// Facts 是创建阶段冻结的描述输入，不包含来源 content 或可变工作区。
type Facts struct {
	ItemName   string
	Unit       string
	Value      Number
	Connector  string
	Algorithms []Algorithm
	NoData     *NoDataEvidence
}

// Render 按已实现的 bk-monitor 规则生成 description；不命中、缺事实或未知算法均报错。
// 连接符显式为 and/or；算法、组及条件顺序有语义，不进行排序。
func Render(f Facts) (string, error) {
	if f.NoData != nil {
		return renderNoData(f)
	}
	if len(f.Algorithms) == 0 || len(f.Algorithms) > maxAlgorithms || !f.Value.valid || len(f.ItemName) > 4096 || !utf8.ValidString(f.ItemName) {
		return "", invalid("facts_invalid")
	}
	if f.Connector != "and" && f.Connector != "or" {
		return "", invalid("connector_invalid")
	}
	u, err := loadUnit(f.Unit)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(f.Algorithms))
	special := false
	for _, algorithm := range f.Algorithms {
		part, matched, standalone, err := renderAlgorithm(f.Value, u, algorithm)
		if algorithm.Type == "TimeSeriesForecasting" {
			part, matched, err = renderForecast(f.ItemName, u, algorithm)
			standalone = true
		}
		if err != nil {
			return "", err
		}
		if !matched {
			if f.Connector == "and" {
				return "", invalid("not_triggered")
			}
			continue
		}
		if len(parts) > 0 && special != standalone {
			return "", invalid("mixed_templates")
		}
		special = standalone
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "", invalid("not_triggered")
	}
	content := strings.Join(parts, "且")
	if !special {
		formatted, err := u.format(f.Value)
		if err != nil {
			return "", err
		}
		content = f.ItemName + content + ", 当前值" + formatted
	}
	if len(content) > maxContentBytes || !utf8.ValidString(content) {
		return "", invalid("content_limit")
	}
	return content, nil
}

func renderAlgorithm(value Number, u unitDefinition, a Algorithm) (string, bool, bool, error) {
	switch a.Type {
	case "Threshold":
		text, matched, err := renderThreshold(value, u, a)
		return text, matched, false, err
	case "PingUnreachable":
		return "Ping不可达", value.value >= 1, true, nil
	case "OsRestart":
		// 只有已触发的专用事实可进入此模板；本包不重算其历史检测条件。
		if a.DetectionMatched == nil {
			return "", false, true, invalid("detection_evidence_missing")
		}
		if !*a.DetectionMatched {
			return "", false, true, nil
		}
		return "当前服务器在" + pythonNumber(value.value, value.integer) + "秒前发生系统重启事件", true, true, nil
	case "ProcPort":
		if len(a.ProcessName) > 4096 || len(a.InaccurateListen) > 4096 || len(a.BindIP) > 256 {
			return "", false, true, invalid("process_facts_invalid")
		}
		if math.Trunc(value.value) != 1 {
			return "当前进程(" + a.ProcessName + ")不存在", true, true, nil
		}
		if len(a.NonListeningPorts) > 0 {
			ports, err := portRange(a.NonListeningPorts)
			if err != nil {
				return "", false, true, err
			}
			return "当前进程(" + a.ProcessName + ")存在，端口(" + ports + ")不存在", true, true, nil
		}
		if a.InaccurateListen != "" && a.InaccurateListen != "[]" && a.InaccurateListen != "null" {
			return "当前进程(" + a.ProcessName + ")和监听端口(" + a.InaccurateListen + ")存在，监听的IP与CMDB中配置的(" + a.BindIP + ")不符", true, true, nil
		}
		return "", false, true, nil
	case "SimpleRingRatio", "SimpleYearRound", "AdvancedRingRatio", "AdvancedYearRound", "RingRatioAmplitude":
		text, hit, err := renderHistory(u, a)
		return text, hit, false, err
	case "TimeSeriesForecasting":
		return "", false, true, invalid("forecast_requires_item")
	default:
		return "", false, false, invalid("algorithm_unsupported")
	}
}

func renderThreshold(value Number, u unitDefinition, a Algorithm) (string, bool, error) {
	if len(a.Groups) == 0 || len(a.Groups) > maxGroups || len(a.UnitPrefix) > 256 {
		return "", false, invalid("threshold_config_invalid")
	}
	for _, group := range a.Groups {
		if len(group) == 0 || len(group) > maxConditions {
			return "", false, invalid("threshold_config_invalid")
		}
		for _, c := range group {
			if math.IsInf(c.Threshold, 0) || math.IsNaN(c.Threshold) {
				return "", false, invalid("threshold_config_invalid")
			}
			switch c.Method {
			case "gt", "gte", "lt", "lte", "eq", "neq":
			default:
				return "", false, invalid("threshold_method_invalid")
			}
		}
	}
	observed := u.toMinimum(value.value, u.index)
	if math.IsInf(observed, 0) || math.IsNaN(observed) {
		return "", false, invalid("unit_overflow")
	}
	for _, group := range a.Groups {
		parts := make([]string, 0, len(group))
		matched := true
		for _, c := range group {
			threshold := u.thresholdToMinimum(c.Threshold, a.UnitPrefix)
			if math.IsInf(threshold, 0) || math.IsNaN(threshold) {
				return "", false, invalid("unit_overflow")
			}
			mark, hit := compare(observed, threshold, c.Method)
			matched = matched && hit
			parts = append(parts, fmt.Sprintf(" %s %s%s", mark, pythonNumber(c.Threshold, false), u.thresholdSuffix(a.UnitPrefix)))
		}
		// BasicAlgorithmsCollection(expr_op=or) 在首个命中组返回。
		if matched {
			return strings.Join(parts, "且"), true, nil
		}
	}
	return "", false, nil
}

func compare(value, threshold float64, method string) (string, bool) {
	switch method {
	case "gt":
		return ">", value > threshold
	case "gte":
		return ">=", value >= threshold
	case "lt":
		return "<", value < threshold
	case "lte":
		return "<=", value <= threshold
	case "eq":
		return "=", value == threshold
	case "neq":
		return "!=", value != threshold
	default:
		return "", false
	}
}

func portRange(input []int) (string, error) {
	if len(input) > 4096 {
		return "", invalid("process_ports_invalid")
	}
	ports := append([]int(nil), input...)
	sort.Ints(ports)
	parts := make([]string, 0, len(ports))
	for i := 0; i < len(ports); {
		start, end := ports[i], ports[i]
		if start < 0 || start > 65535 {
			return "", invalid("process_ports_invalid")
		}
		i++
		// bk-monitor merge_port 保留重复端口，并忽略 0；不能先去重。
		if start == 0 {
			continue
		}
		for i < len(ports) && ports[i] == end+1 {
			end = ports[i]
			i++
		}
		if end > 65535 {
			return "", invalid("process_ports_invalid")
		}
		text := strconv.Itoa(start)
		if end != start {
			text += "-" + strconv.Itoa(end)
		}
		parts = append(parts, text)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}
