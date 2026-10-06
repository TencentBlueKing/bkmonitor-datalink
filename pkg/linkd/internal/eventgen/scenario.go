// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"

	linkdconfig "linkd/internal/config"
)

const (
	ScenarioCPUHigh                 Scenario = "cpu_high"
	ScenarioMemoryHigh              Scenario = "memory_high"
	ScenarioDiskFull                Scenario = "disk_full"
	ScenarioDiskReadOnly            Scenario = "disk_read_only"
	ScenarioDiskIOLatencyHigh       Scenario = "disk_io_latency_high"
	ScenarioOOMKilled               Scenario = "oom_killed"
	ScenarioProcessDown             Scenario = "process_down"
	ScenarioHostUnreachable         Scenario = "host_unreachable"
	ScenarioNetworkPacketLossHigh   Scenario = "network_packet_loss_high"
	ScenarioServiceUnavailable      Scenario = "service_unavailable"
	ScenarioHTTPErrorRateHigh       Scenario = "http_error_rate_high"
	ScenarioDatabaseConnectionsHigh Scenario = "database_connections_high"
	ScenarioOnlineUsersZero         Scenario = "online_users_zero"
	ScenarioQueueBacklogHigh        Scenario = "queue_backlog_high"
)

var supportedScenarios = []Scenario{
	ScenarioCPUHigh,
	ScenarioMemoryHigh,
	ScenarioDiskFull,
	ScenarioDiskReadOnly,
	ScenarioDiskIOLatencyHigh,
	ScenarioOOMKilled,
	ScenarioProcessDown,
	ScenarioHostUnreachable,
	ScenarioNetworkPacketLossHigh,
	ScenarioServiceUnavailable,
	ScenarioHTTPErrorRateHigh,
	ScenarioDatabaseConnectionsHigh,
	ScenarioOnlineUsersZero,
	ScenarioQueueBacklogHigh,
}

// Scenario 标识一种内置的常见告警模板。
type Scenario string

// Valid 报告场景是否由 eventgen 实现。
func (s Scenario) Valid() bool {
	for _, candidate := range supportedScenarios {
		if s == candidate {
			return true
		}
	}
	return false
}

// SupportedScenarios 返回按稳定顺序排列的内置场景。
func SupportedScenarios() []Scenario {
	return append([]Scenario(nil), supportedScenarios...)
}

// SupportedScenariosCSV 返回适合作为命令行默认值展示的场景列表。
func SupportedScenariosCSV() string {
	values := make([]string, len(supportedScenarios))
	for index, scenario := range supportedScenarios {
		values[index] = string(scenario)
	}
	return strings.Join(values, ",")
}

// ParseScenarios 严格解析逗号分隔且不重复的场景清单。
func ParseScenarios(value string) ([]Scenario, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("scenarios must not be empty")
	}
	parts := strings.Split(value, ",")
	result := make([]Scenario, 0, len(parts))
	seen := make(map[Scenario]struct{}, len(parts))
	for _, part := range parts {
		scenario := Scenario(strings.TrimSpace(part))
		if !scenario.Valid() {
			return nil, fmt.Errorf("unsupported scenario %q", scenario)
		}
		if _, exists := seen[scenario]; exists {
			return nil, fmt.Errorf("scenario %q is duplicated", scenario)
		}
		seen[scenario] = struct{}{}
		result = append(result, scenario)
	}
	return result, nil
}

type randomSource interface {
	Uint64N(n uint64) uint64
}

type alertTemplate struct {
	Scenario      Scenario
	AlertID       string
	Title         string
	ConditionName string
	Severity      string
	Dimensions    map[string]any
	Subject       standardSubject
	Labels        map[string]any
	TriggerExtra  map[string]any
	ResolvedExtra map[string]any
}

func newRandomSource(seed uint64) randomSource {
	// PRNG 只用于可复现的模拟场景和生命周期抽样，不用于身份、凭据或安全决策。
	//nolint:gosec // G404: eventgen 明确需要接受 seed 的确定性伪随机序列。
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

func buildAlertTemplate(
	scenario Scenario,
	sequence uint64,
	runID string,
	severity linkdconfig.SeverityConfig,
	random randomSource,
) (alertTemplate, error) {
	if !scenario.Valid() {
		return alertTemplate{}, fmt.Errorf("unsupported scenario %q", scenario)
	}
	generatorID := fmt.Sprintf("sim-%s-a%012d", runID, sequence)
	ip := patternedIP(sequence)
	dimensions := map[string]any{"generator_id": generatorID}
	labels := map[string]any{"generator": "linkd-eventgen", "scenario": string(scenario)}
	template := alertTemplate{
		Scenario: scenario, AlertID: generatorID, Severity: scenarioSeverity(severity, scenario),
		Dimensions: dimensions, Subject: standardSubject{ID: generatorID}, Labels: labels,
	}

	switch scenario {
	case ScenarioCPUHigh:
		template.Title, template.ConditionName = "主机 CPU 使用率过高", "CPU 使用率"
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "host", "host-"+ip
		// Uint64N 的结果严格小于 32，可安全转换为 int。
		//nolint:gosec // G115: 取值上界保证转换安全。
		cpuCore := int(random.Uint64N(32))
		dimensions["ip"], dimensions["cpu_core"] = ip, cpuCore
		template.TriggerExtra, template.ResolvedExtra = metricPair("cpu_usage", 90+fraction(random, 9), 50+fraction(random, 20), 85, "percent")
	case ScenarioMemoryHigh:
		template.Title, template.ConditionName = "主机内存使用率过高", "内存使用率"
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "host", "host-"+ip
		// Uint64N 的结果严格小于 8，可安全转换为 int。
		//nolint:gosec // G115: 取值上界保证转换安全。
		memoryClass := int(random.Uint64N(8))
		dimensions["ip"], dimensions["memory_total_gb"] = ip, 16*(1+memoryClass)
		template.TriggerExtra, template.ResolvedExtra = metricPair("memory_usage", 90+fraction(random, 8), 55+fraction(random, 20), 85, "percent")
	case ScenarioDiskFull:
		template.Title, template.ConditionName = "磁盘空间即将写满", "磁盘使用率"
		device, mount := diskIdentity(sequence)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "disk", ip+":"+mount
		dimensions["ip"], dimensions["device"], dimensions["mount_point"], dimensions["filesystem"] = ip, device, mount, "xfs"
		template.TriggerExtra, template.ResolvedExtra = metricPair("disk_usage", 96+fraction(random, 3), 60+fraction(random, 20), 95, "percent")
	case ScenarioDiskReadOnly:
		template.Title, template.ConditionName = "磁盘文件系统只读", "文件系统写入状态"
		device, mount := diskIdentity(sequence)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "disk", ip+":"+mount
		dimensions["ip"], dimensions["device"], dimensions["mount_point"], dimensions["filesystem"] = ip, device, mount, "ext4"
		template.TriggerExtra = stateExtra("filesystem_read_only", true, "read_only")
		template.ResolvedExtra = stateExtra("filesystem_read_only", false, "read_write")
	case ScenarioDiskIOLatencyHigh:
		template.Title, template.ConditionName = "磁盘 IO 延迟过高", "磁盘写入延迟"
		device, mount := diskIdentity(sequence)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "disk", ip+":"+device
		dimensions["ip"], dimensions["device"], dimensions["mount_point"] = ip, device, mount
		template.TriggerExtra, template.ResolvedExtra = metricPair("disk_write_latency", 80+fraction(random, 120), 5+fraction(random, 20), 50, "ms")
	case ScenarioOOMKilled:
		template.Title, template.ConditionName = "容器发生 OOM", "OOM Kill"
		namespace := "namespace-" + strconv.Itoa(1+int(sequence%12))
		pod := fmt.Sprintf("worker-%06d", sequence%1_000_000)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "kubernetes", "container", namespace+"/"+pod
		dimensions["ip"], dimensions["namespace"], dimensions["pod"], dimensions["container"], dimensions["process"] = ip, namespace, pod, "app", "worker"
		template.TriggerExtra = stateExtra("oom_killed", true, "killed")
		template.ResolvedExtra = stateExtra("oom_killed", false, "running")
	case ScenarioProcessDown:
		template.Title, template.ConditionName = "关键进程退出", "进程存活状态"
		process := []string{"nginx", "mysqld", "redis-server", "linkd"}[sequence%4]
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "process", ip+":"+process
		dimensions["ip"], dimensions["process_name"], dimensions["service_name"] = ip, process, process
		template.TriggerExtra = stateExtra("process_up", 0, "down")
		template.ResolvedExtra = stateExtra("process_up", 1, "up")
	case ScenarioHostUnreachable:
		template.Title, template.ConditionName = "主机不可达", "主机连通性"
		zone := "zone-" + strconv.Itoa(1+int(sequence%6))
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "host", "host-"+ip
		dimensions["ip"], dimensions["zone"] = ip, zone
		template.TriggerExtra, template.ResolvedExtra = metricPair("ping_packet_loss", 100, 0, 80, "percent")
	case ScenarioNetworkPacketLossHigh:
		template.Title, template.ConditionName = "网络丢包率过高", "网络丢包率"
		peer := patternedIP(sequence + 1_000_000)
		iface := "eth" + strconv.Itoa(int(sequence%4))
		template.Subject.System, template.Subject.Type, template.Subject.Name = "cmdb", "network_interface", ip+":"+iface
		dimensions["ip"], dimensions["peer_ip"], dimensions["interface"] = ip, peer, iface
		template.TriggerExtra, template.ResolvedExtra = metricPair("packet_loss", 15+fraction(random, 45), fraction(random, 2), 10, "percent")
	case ScenarioServiceUnavailable:
		template.Title, template.ConditionName = "服务实例不可用", "服务可用性"
		service := []string{"api", "gateway", "scheduler", "worker"}[sequence%4]
		port := 8000 + int(sequence%1000)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "service", "service_instance", service+"@"+ip
		dimensions["service"], dimensions["instance"], dimensions["ip"], dimensions["port"], dimensions["protocol"] = service, generatorID, ip, port, "tcp"
		template.TriggerExtra = stateExtra("service_up", 0, "unavailable")
		template.ResolvedExtra = stateExtra("service_up", 1, "available")
	case ScenarioHTTPErrorRateHigh:
		template.Title, template.ConditionName = "HTTP 错误率过高", "HTTP 5xx 错误率"
		service := []string{"api", "gateway", "console"}[sequence%3]
		route := []string{"/api/v1/events", "/api/v1/alerts", "/healthz"}[sequence%3]
		template.Subject.System, template.Subject.Type, template.Subject.Name = "service", "http_route", service+route
		dimensions["service"], dimensions["route"], dimensions["method"], dimensions["status_class"] = service, route, "GET", "5xx"
		template.TriggerExtra, template.ResolvedExtra = metricPair("http_error_rate", 10+fraction(random, 25), fraction(random, 2), 5, "percent")
	case ScenarioDatabaseConnectionsHigh:
		template.Title, template.ConditionName = "数据库连接数过高", "数据库连接使用率"
		engine := []string{"mysql", "postgresql"}[sequence%2]
		instance := fmt.Sprintf("%s-%06d", engine, sequence%1_000_000)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "database", "database_instance", instance
		dimensions["db_instance"], dimensions["engine"], dimensions["region"] = instance, engine, region(sequence)
		template.TriggerExtra, template.ResolvedExtra = metricPair("connection_usage", 92+fraction(random, 7), 45+fraction(random, 25), 85, "percent")
	case ScenarioOnlineUsersZero:
		template.Title, template.ConditionName = "在线人数掉零", "在线用户数"
		app := []string{"portal", "console", "mobile"}[sequence%3]
		template.Subject.System, template.Subject.Type, template.Subject.Name = "application", "application", app+"@"+region(sequence)
		dimensions["app"], dimensions["region"], dimensions["channel"] = app, region(sequence), "realtime"
		template.TriggerExtra, template.ResolvedExtra = metricPair("online_users", 0, 10+float64(random.Uint64N(5000)), 1, "count")
	case ScenarioQueueBacklogHigh:
		template.Title, template.ConditionName = "消息队列积压过高", "消费积压"
		queue := fmt.Sprintf("events-%02d", sequence%32)
		group := fmt.Sprintf("consumer-%02d", sequence%16)
		template.Subject.System, template.Subject.Type, template.Subject.Name = "message_queue", "consumer_group", queue+":"+group
		dimensions["queue"], dimensions["consumer_group"], dimensions["cluster"] = queue, group, "mq-"+region(sequence)
		template.TriggerExtra, template.ResolvedExtra = metricPair("consumer_lag", 10_000+float64(random.Uint64N(90_000)), float64(random.Uint64N(100)), 5_000, "messages")
	}
	return template, nil
}

func scenarioSeverity(cfg linkdconfig.SeverityConfig, scenario Scenario) string {
	levels := cfg.WithDefaults().Levels
	sort.Slice(levels, func(left, right int) bool { return levels[left].Priority < levels[right].Priority })
	index := 1
	switch scenario {
	case ScenarioDiskFull, ScenarioDiskReadOnly, ScenarioOOMKilled, ScenarioHostUnreachable,
		ScenarioServiceUnavailable, ScenarioOnlineUsersZero:
		index = 0
	}
	if index >= len(levels) {
		index = 0
	}
	return levels[index].Name
}

func metricPair(name string, firing, resolved, threshold float64, unit string) (map[string]any, map[string]any) {
	return map[string]any{
			"metric_name": name, "value": firing, "threshold": threshold, "unit": unit, "state": "firing",
		}, map[string]any{
			"metric_name": name, "value": resolved, "threshold": threshold, "unit": unit, "state": "resolved",
		}
}

func stateExtra(name string, value any, state string) map[string]any {
	return map[string]any{"metric_name": name, "value": value, "state": state}
}

func fraction(random randomSource, limit uint64) float64 {
	return float64(random.Uint64N(limit*10)) / 10
}

func patternedIP(sequence uint64) string {
	third := (sequence / 254) % 254
	fourth := sequence%254 + 1
	second := (sequence/(254*254))%254 + 1
	return fmt.Sprintf("10.%d.%d.%d", second, third, fourth)
}

func diskIdentity(sequence uint64) (string, string) {
	device := fmt.Sprintf("/dev/nvme%dn1", sequence%8)
	mount := fmt.Sprintf("/data/%02d", sequence%32)
	return device, mount
}

func region(sequence uint64) string {
	return []string{"ap-guangzhou", "ap-shanghai", "ap-beijing", "ap-singapore"}[sequence%4]
}
