package description

import (
	"encoding/json"
	"strings"

	"linkd/internal/domain"
)

// resolveProcessFacts 只读取 alarmd 合并后的进程/端口维度，不从来源文案
// 反推进程名。当前点不能解释窗口触发、或展示事实缺失时由调用方降级。
func resolveProcessFacts(event domain.Event, value Number) (Algorithm, error) {
	facts := Algorithm{Type: "ProcPort"}
	text := func(key string, limit int) (string, error) {
		scalar, ok := event.Dimensions[key]
		if !ok {
			return "", invalid("process_evidence_missing")
		}
		result, ok := scalar.StringValue()
		if !ok || len(result) > limit {
			return "", invalid("process_facts_invalid")
		}
		if strings.TrimSpace(result) == "" {
			return "", invalid("process_evidence_missing")
		}
		return result, nil
	}
	name, err := text("display_name", 4096)
	if err != nil {
		return facts, err
	}
	facts.ProcessName = name
	if value.value != 0 && value.value != 1 {
		return facts, invalid("process_facts_invalid")
	}
	if value.value == 0 {
		return facts, nil
	}
	ports := func(key string) ([]int, error) {
		raw, err := text(key, 4096)
		if err != nil {
			return nil, err
		}
		var values []int
		if json.Unmarshal([]byte(raw), &values) != nil || values == nil || len(values) > 4096 {
			return nil, invalid("process_facts_invalid")
		}
		for _, port := range values {
			if port <= 0 || port > 65535 {
				return nil, invalid("process_facts_invalid")
			}
		}
		return values, nil
	}
	facts.NonListeningPorts, err = ports("nonlisten")
	if err != nil {
		return facts, err
	}
	if len(facts.NonListeningPorts) > 0 {
		return facts, nil
	}
	inaccurate, err := ports("not_accurate_listen")
	if err != nil {
		return facts, err
	}
	if len(inaccurate) == 0 {
		return facts, invalid("process_evidence_missing")
	}
	facts.BindIP, err = text("bind_ip", 256)
	if err != nil {
		return facts, err
	}
	raw, err := json.Marshal(inaccurate)
	if err != nil {
		return facts, err
	}
	facts.InaccurateListen = string(raw)
	return facts, nil
}
