// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rawgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
)

const (
	// ScenarioActive 只生成 triggered，最终保留一个活动 Alert。
	ScenarioActive ScenarioType = "active"
	// ScenarioRecovered 生成 triggered、若干同等级 triggered 和 resolved。
	ScenarioRecovered ScenarioType = "recovered"
	// ScenarioClosed 生成 triggered、若干同等级 triggered 和 closed。
	ScenarioClosed ScenarioType = "closed"
	// ScenarioSeverityRotation 生成 warning→critical 等级轮转并最终关闭。
	ScenarioSeverityRotation ScenarioType = "severity_rotation"
	// ScenarioCrossTenant 使用相同来源 alert_id 在两个租户各生成一条活动告警。
	ScenarioCrossTenant ScenarioType = "cross_tenant"

	defaultEventSourceID = "e2e-source"
	defaultTenantPrefix  = "tenant-load"
	maxScenarioCount     = 1_000_000
	maxRecordCount       = 5_000_000
)

var supportedScenarioTypes = []ScenarioType{
	ScenarioActive,
	ScenarioRecovered,
	ScenarioClosed,
	ScenarioSeverityRotation,
	ScenarioCrossTenant,
}

// ScenarioType 标识一个会产生确定生命周期形态的测试场景。
type ScenarioType string

// Config 描述数据规模、场景配额、随机种子和稳定身份命名。
type Config struct {
	Seed             uint64               `json:"seed"`
	EventSourceID    string               `json:"event_source_id"`
	TenantPrefix     string               `json:"tenant_prefix"`
	TenantCount      int                  `json:"tenant_count"`
	Counts           map[ScenarioType]int `json:"counts"`
	DuplicateRecords int                  `json:"duplicate_records"`
	InvalidRecords   int                  `json:"invalid_records"`
	MinUpdates       int                  `json:"min_updates"`
	MaxUpdates       int                  `json:"max_updates"`
	StartTime        time.Time            `json:"start_time"`
}

// Record 是一条待写入 Kafka 的 message，包含业务 value 及保证生命周期分区/时间稳定的 metadata。
type Record struct {
	Scenario       ScenarioType
	BKTenantID     string
	KafkaKey       string
	KafkaTimestamp time.Time
	Body           []byte
	Valid          bool
	SourceEventID  string
}

// ExpectedAlert 描述生成数据处理完成后应存在的 Alert 集合项。
type ExpectedAlert struct {
	BKTenantID  string             `json:"bk_tenant_id"`
	Fingerprint string             `json:"fingerprint"`
	Severity    string             `json:"severity"`
	Status      domain.AlertStatus `json:"status"`
	Count       int                `json:"count"`
}

// Expected 汇总下游可观察数量和身份，供 E2E、压测抽样及结果核对复用。
type Expected struct {
	SourceEventIDs     []string                            `json:"source_event_ids"`
	EventStates        map[string]domain.EventProcessState `json:"event_states"`
	FallbackReceivedAt map[string]time.Time                `json:"fallback_received_time"`
	InputRecords       int                                 `json:"input_records"`
	OutputMessages     int                                 `json:"output_messages"`
	Alerts             []ExpectedAlert                     `json:"alerts"`
	OperationCounts    map[domain.OperationKind]int        `json:"operation_counts"`
}

// Dataset 同时返回 Kafka 输入记录、规范化后的配置和可计算预期结果。
type Dataset struct {
	Config   Config   `json:"config"`
	Records  []Record `json:"-"`
	Expected Expected `json:"expected"`
}

// Generate 按 seed 随机排列生命周期块，但保持每个块内部事件顺序，生成可复现数据集。
func Generate(input Config) (Dataset, error) {
	config := input.withDefaults()
	if err := config.Validate(); err != nil {
		return Dataset{}, err
	}
	random := newDeterministicRandom(config.Seed)
	plans := make([]ScenarioType, 0, config.scenarioCount())
	for _, scenario := range supportedScenarioTypes {
		for range config.Counts[scenario] {
			plans = append(plans, scenario)
		}
	}
	random.shuffle(plans)
	generator := generator{
		config: config,
		random: random,
		expected: Expected{
			FallbackReceivedAt: make(map[string]time.Time),
			EventStates:        make(map[string]domain.EventProcessState),
			OperationCounts:    make(map[domain.OperationKind]int),
		},
	}
	maxRecords, err := config.maxRecordEstimate()
	if err != nil {
		return Dataset{}, err
	}
	baseCapacity := maxRecords - config.DuplicateRecords - config.InvalidRecords
	baseRecords := make([]Record, 0, baseCapacity)
	for index, scenario := range plans {
		records, err := generator.generateScenario(scenario, index+1)
		if err != nil {
			return Dataset{}, err
		}
		baseRecords = append(baseRecords, records...)
	}
	if len(baseRecords) == 0 && config.DuplicateRecords != 0 {
		return Dataset{}, fmt.Errorf("generate standard dataset: duplicates require at least one valid event")
	}
	withDuplicates := injectDuplicates(baseRecords, config.DuplicateRecords, random)
	records := injectInvalidRecords(withDuplicates, config.InvalidRecords, config.StartTime, random)
	generator.expected.InputRecords = len(records)
	for _, state := range generator.expected.EventStates {
		if state == domain.EventProcessStateAccepted {
			generator.expected.OutputMessages++
		}
	}
	// 每次等级升级由同一 Event 产生旧 Alert closed 和新 Alert active 两个快照。
	generator.expected.OutputMessages += config.Counts[ScenarioSeverityRotation]
	generator.expected.OperationCounts[domain.OperationKindPush] = generator.expected.OutputMessages
	return Dataset{Config: config, Records: records, Expected: generator.expected}, nil
}

// BalancedCounts 把指定场景总数尽量平均地分配到 types；seed 影响后续生成顺序和字段值。
func BalancedCounts(total int, types []ScenarioType) (map[ScenarioType]int, error) {
	if total < 0 {
		return nil, fmt.Errorf("scenario total must not be negative")
	}
	if len(types) == 0 && total != 0 {
		return nil, fmt.Errorf("scenario types must not be empty")
	}
	counts := make(map[ScenarioType]int, len(types))
	for _, scenario := range types {
		if !scenario.Valid() {
			return nil, fmt.Errorf("unsupported scenario type %q", scenario)
		}
		if _, exists := counts[scenario]; exists {
			return nil, fmt.Errorf("scenario type %q is duplicated", scenario)
		}
		counts[scenario] = 0
	}
	for index := 0; index < total; index++ {
		counts[types[index%len(types)]]++
	}
	return counts, nil
}

// ParseScenarioTypes 解析逗号分隔且不重复的场景类型。
func ParseScenarioTypes(value string) ([]ScenarioType, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("scenario types must not be empty")
	}
	parts := strings.Split(value, ",")
	types := make([]ScenarioType, 0, len(parts))
	seen := make(map[ScenarioType]struct{}, len(parts))
	for _, part := range parts {
		scenario := ScenarioType(strings.TrimSpace(part))
		if !scenario.Valid() {
			return nil, fmt.Errorf("unsupported scenario type %q", scenario)
		}
		if _, exists := seen[scenario]; exists {
			return nil, fmt.Errorf("scenario type %q is duplicated", scenario)
		}
		seen[scenario] = struct{}{}
		types = append(types, scenario)
	}
	return types, nil
}

// ParseMix 解析 active=10,recovered=20 形式的精确场景配额。
func ParseMix(value string) (map[ScenarioType]int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("scenario mix must not be empty")
	}
	counts := make(map[ScenarioType]int)
	for _, item := range strings.Split(value, ",") {
		name, countText, found := strings.Cut(strings.TrimSpace(item), "=")
		scenario := ScenarioType(name)
		if !found || !scenario.Valid() {
			return nil, fmt.Errorf("invalid scenario mix item %q", item)
		}
		if _, exists := counts[scenario]; exists {
			return nil, fmt.Errorf("scenario type %q is duplicated", scenario)
		}
		count, err := strconv.Atoi(countText)
		if err != nil || count < 0 {
			return nil, fmt.Errorf("scenario %q count must be a non-negative integer", scenario)
		}
		counts[scenario] = count
	}
	return counts, nil
}

// Valid 报告场景类型是否由生成器实现。
func (s ScenarioType) Valid() bool {
	for _, supported := range supportedScenarioTypes {
		if s == supported {
			return true
		}
	}
	return false
}

// SupportedScenarioTypes 返回固定顺序的场景类型副本。
func SupportedScenarioTypes() []ScenarioType {
	return append([]ScenarioType(nil), supportedScenarioTypes...)
}

// Validate 校验生成规模、场景配额和时间边界。
func (c Config) Validate() error {
	if strings.TrimSpace(c.EventSourceID) != c.EventSourceID || c.EventSourceID == "" {
		return fmt.Errorf("event_source_id must not be empty or contain surrounding whitespace")
	}
	if strings.TrimSpace(c.TenantPrefix) != c.TenantPrefix || c.TenantPrefix == "" {
		return fmt.Errorf("tenant_prefix must not be empty or contain surrounding whitespace")
	}
	if c.TenantCount < 1 {
		return fmt.Errorf("tenant_count must be positive")
	}
	if c.DuplicateRecords < 0 || c.InvalidRecords < 0 {
		return fmt.Errorf("duplicate_records and invalid_records must not be negative")
	}
	if c.MinUpdates < 0 || c.MaxUpdates < c.MinUpdates {
		return fmt.Errorf("update range must satisfy 0 <= min_updates <= max_updates")
	}
	if c.StartTime.IsZero() {
		return fmt.Errorf("start_time must not be zero")
	}
	total := 0
	for scenario, count := range c.Counts {
		if !scenario.Valid() {
			return fmt.Errorf("unsupported scenario type %q", scenario)
		}
		if count < 0 {
			return fmt.Errorf("scenario %q count must not be negative", scenario)
		}
		if scenario == ScenarioCrossTenant && count != 0 && c.TenantCount < 2 {
			return fmt.Errorf("cross_tenant scenarios require at least two tenants")
		}
		if count > maxScenarioCount-total {
			return fmt.Errorf("scenario count exceeds %d", maxScenarioCount)
		}
		total += count
	}
	if _, err := c.maxRecordEstimate(); err != nil {
		return err
	}
	return nil
}

// maxRecordEstimate 在展开场景前计算最坏记录数，避免恶意或误配的 updates 先触发超量分配。
func (c Config) maxRecordEstimate() (int, error) {
	total := 0
	for scenario, count := range c.Counts {
		perScenario := 1
		switch scenario {
		case ScenarioRecovered, ScenarioClosed:
			if c.MaxUpdates > maxRecordCount-2 {
				return 0, fmt.Errorf("record count exceeds %d", maxRecordCount)
			}
			perScenario = c.MaxUpdates + 2
		case ScenarioSeverityRotation:
			if c.MaxUpdates > maxRecordCount-3 {
				return 0, fmt.Errorf("record count exceeds %d", maxRecordCount)
			}
			perScenario = c.MaxUpdates + 3
		case ScenarioCrossTenant:
			perScenario = 2
		}
		if count != 0 && count > (maxRecordCount-total)/perScenario {
			return 0, fmt.Errorf("record count exceeds %d", maxRecordCount)
		}
		total += count * perScenario
	}
	for _, extra := range []int{c.DuplicateRecords, c.InvalidRecords} {
		if extra > maxRecordCount-total {
			return 0, fmt.Errorf("record count exceeds %d", maxRecordCount)
		}
		total += extra
	}
	return total, nil
}

func (c Config) withDefaults() Config {
	normalized := c
	if normalized.EventSourceID == "" {
		normalized.EventSourceID = defaultEventSourceID
	}
	if normalized.TenantPrefix == "" {
		normalized.TenantPrefix = defaultTenantPrefix
	}
	if normalized.TenantCount == 0 {
		normalized.TenantCount = 1
	}
	if normalized.StartTime.IsZero() {
		normalized.StartTime = time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	}
	normalized.StartTime = normalized.StartTime.Round(0).UTC()
	if normalized.Counts == nil {
		normalized.Counts = map[ScenarioType]int{}
	} else {
		cloned := make(map[ScenarioType]int, len(normalized.Counts))
		for scenario, count := range normalized.Counts {
			cloned[scenario] = count
		}
		normalized.Counts = cloned
	}
	return normalized
}

func (c Config) scenarioCount() int {
	total := 0
	for _, count := range c.Counts {
		total += count
	}
	return total
}

type generator struct {
	config       Config
	random       *deterministicRandom
	eventCounter int
	expected     Expected
}

func (g *generator) generateScenario(scenario ScenarioType, index int) ([]Record, error) {
	alertID := fmt.Sprintf("load-alert-%08d", index)
	tenantID := g.tenantID(g.random.intn(g.config.TenantCount))
	updates := g.config.MinUpdates
	if delta := g.config.MaxUpdates - g.config.MinUpdates; delta != 0 {
		updates += g.random.intn(delta + 1)
	}
	switch scenario {
	case ScenarioActive:
		severity := g.randomSeverity()
		record, err := g.eventRecord(scenario, index, tenantID, alertID, severity, domain.EventActionTriggered, time.Time{})
		if err != nil {
			return nil, err
		}
		g.addExpectedAlert(tenantID, alertID, severity, domain.AlertStatusActive)
		g.expected.OperationCounts[domain.OperationKindTrigger]++
		return []Record{record}, nil
	case ScenarioRecovered, ScenarioClosed:
		severity := g.randomSeverity()
		return g.terminalScenario(scenario, index, tenantID, alertID, severity, updates)
	case ScenarioSeverityRotation:
		return g.rotationScenario(index, tenantID, alertID, updates)
	case ScenarioCrossTenant:
		return g.crossTenantScenario(index, alertID)
	default:
		return nil, fmt.Errorf("unsupported scenario type %q", scenario)
	}
}

func (g *generator) terminalScenario(
	scenario ScenarioType,
	index int,
	tenantID, alertID, severity string,
	updates int,
) ([]Record, error) {
	startTime := g.nextOccurredTime()
	records := make([]Record, 0, updates+2)
	trigger, err := g.eventRecord(scenario, index, tenantID, alertID, severity, domain.EventActionTriggered, startTime)
	if err != nil {
		return nil, err
	}
	records = append(records, trigger)
	for range updates {
		updated, updateErr := g.eventRecord(scenario, index, tenantID, alertID, severity, domain.EventActionTriggered, startTime)
		if updateErr != nil {
			return nil, updateErr
		}
		records = append(records, updated)
	}
	action := domain.EventActionResolved
	status := domain.AlertStatusRecovered
	operation := domain.OperationKindRecover
	if scenario == ScenarioClosed {
		action = domain.EventActionClosed
		status = domain.AlertStatusClosed
		operation = domain.OperationKindClose
	}
	terminal, err := g.eventRecord(scenario, index, tenantID, alertID, severity, action, startTime)
	if err != nil {
		return nil, err
	}
	records = append(records, terminal)
	g.addExpectedAlert(tenantID, alertID, severity, status)
	g.expected.OperationCounts[domain.OperationKindTrigger]++
	g.expected.OperationCounts[operation]++
	return records, nil
}

func (g *generator) rotationScenario(index int, tenantID, alertID string, updates int) ([]Record, error) {
	startTime := g.nextOccurredTime()
	records := make([]Record, 0, updates+4)
	trigger, err := g.eventRecord(
		ScenarioSeverityRotation, index, tenantID, alertID, "warning", domain.EventActionTriggered, startTime,
	)
	if err != nil {
		return nil, err
	}
	records = append(records, trigger)
	for range updates {
		updated, updateErr := g.eventRecord(
			ScenarioSeverityRotation, index, tenantID, alertID, "warning", domain.EventActionTriggered, startTime,
		)
		if updateErr != nil {
			return nil, updateErr
		}
		records = append(records, updated)
	}
	rotated, err := g.eventRecord(
		ScenarioSeverityRotation, index, tenantID, alertID, "critical", domain.EventActionTriggered, startTime,
	)
	if err != nil {
		return nil, err
	}
	records = append(records, rotated)
	suppressed, err := g.eventRecord(
		ScenarioSeverityRotation, index, tenantID, alertID, "warning", domain.EventActionTriggered, startTime,
	)
	if err != nil {
		return nil, err
	}
	records = append(records, suppressed)
	g.expected.EventStates[suppressed.SourceEventID] = domain.EventProcessStateSuppressed
	closed, err := g.eventRecord(
		ScenarioSeverityRotation, index, tenantID, alertID, "critical", domain.EventActionClosed, startTime,
	)
	if err != nil {
		return nil, err
	}
	records = append(records, closed)
	g.addExpectedAlert(tenantID, alertID, "warning", domain.AlertStatusClosed)
	g.addExpectedAlert(tenantID, alertID, "critical", domain.AlertStatusClosed)
	g.expected.OperationCounts[domain.OperationKindTrigger] += 2
	g.expected.OperationCounts[domain.OperationKindClose] += 2
	g.expected.OperationCounts[domain.OperationKindSuppress]++
	return records, nil
}

func (g *generator) crossTenantScenario(index int, alertID string) ([]Record, error) {
	records := make([]Record, 0, 2)
	for tenantIndex := range 2 {
		tenantID := g.tenantID(tenantIndex)
		record, err := g.eventRecord(
			ScenarioCrossTenant, index, tenantID, alertID, "warning", domain.EventActionTriggered, time.Time{},
		)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
		g.addExpectedAlert(tenantID, alertID, "warning", domain.AlertStatusActive)
		g.expected.OperationCounts[domain.OperationKindTrigger]++
	}
	return records, nil
}

func (g *generator) eventRecord(
	scenario ScenarioType,
	scenarioIndex int,
	tenantID, alertID, severity string,
	action domain.EventAction,
	startTime time.Time,
) (Record, error) {
	g.eventCounter++
	sourceEventID := fmt.Sprintf("load-event-%08d", g.eventCounter)
	occurredTime := g.nextOccurredTime()
	if startTime.IsZero() && action == domain.EventActionTriggered {
		startTime = occurredTime
	}
	receivedTime := occurredTime.Add(time.Second)
	usage, err := domain.NewNumberScalar(float64(50 + g.random.intn(50)))
	if err != nil {
		return Record{}, err
	}
	raw := standardRecord{
		EventID: sourceEventID, AlertID: alertID,
		Title:    fmt.Sprintf("%s %s", scenario, action),
		Content:  fmt.Sprintf("generated seed=%d event=%d", g.config.Seed, g.eventCounter),
		Severity: severity, Action: action, ConditionKey: "cpu",
		Dimensions: domain.DimensionMap{
			"host":     domain.NewStringScalar(fmt.Sprintf("host-%04d", scenarioIndex)),
			"scenario": domain.NewStringScalar(string(scenario)),
			"usage":    usage,
		},
		Subject: standardSubject{
			System: "cmdb", Type: "host", ID: strconv.Itoa(scenarioIndex + 1000),
			Name: fmt.Sprintf("host-%04d", scenarioIndex),
		},
		OccurredAt: occurredTime, ProducedAt: occurredTime.Add(time.Second),
		Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{},
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return Record{}, err
	}
	g.expected.SourceEventIDs = append(g.expected.SourceEventIDs, sourceEventID)
	g.expected.EventStates[sourceEventID] = domain.EventProcessStateAccepted
	return Record{
		Scenario: scenario, BKTenantID: tenantID, KafkaKey: alertID, KafkaTimestamp: receivedTime,
		Body: body, Valid: true, SourceEventID: sourceEventID,
	}, nil
}

func (g *generator) addExpectedAlert(
	tenantID, fingerprint, severity string,
	status domain.AlertStatus,
) {
	g.expected.Alerts = append(g.expected.Alerts, ExpectedAlert{
		BKTenantID: tenantID, Fingerprint: fingerprint, Severity: severity, Status: status, Count: 1,
	})
}

func (g *generator) tenantID(index int) string {
	return fmt.Sprintf("%s-%03d", g.config.TenantPrefix, index+1)
}

func (g *generator) randomSeverity() string {
	if g.random.intn(2) == 0 {
		return "warning"
	}
	return "critical"
}

func (g *generator) nextOccurredTime() time.Time {
	return g.config.StartTime.Add(time.Duration(g.eventCounter+1) * time.Second)
}

type standardRecord struct {
	EventID      string              `json:"event_id"`
	AlertID      string              `json:"alert_id"`
	Title        string              `json:"title"`
	Content      string              `json:"content"`
	Severity     string              `json:"severity"`
	Action       domain.EventAction  `json:"action"`
	ConditionKey string              `json:"condition_key"`
	Dimensions   domain.DimensionMap `json:"dimensions"`
	Subject      standardSubject     `json:"subject"`
	OccurredAt   time.Time           `json:"occurred_at"`
	ProducedAt   time.Time           `json:"produced_at"`
	Labels       domain.DimensionMap `json:"labels"`
	ExtraData    domain.JSONObject   `json:"extra_data"`
}

type standardSubject struct {
	System string `json:"system"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name"`
}

func injectDuplicates(records []Record, count int, random *deterministicRandom) []Record {
	duplicates := make(map[int]int)
	for range count {
		duplicates[random.intn(len(records))]++
	}
	result := make([]Record, 0, len(records)+count)
	for index, record := range records {
		result = append(result, record)
		for range duplicates[index] {
			duplicate := record
			duplicate.Body = append([]byte(nil), record.Body...)
			result = append(result, duplicate)
		}
	}
	return result
}

func injectInvalidRecords(
	records []Record,
	count int,
	startTime time.Time,
	random *deterministicRandom,
) []Record {
	invalidBySlot := make(map[int]int)
	for range count {
		invalidBySlot[random.intn(len(records)+1)]++
	}
	result := make([]Record, 0, len(records)+count)
	invalidIndex := 0
	for slot := 0; slot <= len(records); slot++ {
		for range invalidBySlot[slot] {
			invalidIndex++
			result = append(result, Record{
				Scenario: "invalid", KafkaKey: "invalid",
				KafkaTimestamp: startTime.Add(time.Duration(invalidIndex) * time.Millisecond),
				Body:           []byte(fmt.Sprintf(`{"record_id":"invalid-%08d","unknown":true}`, invalidIndex)),
			})
		}
		if slot < len(records) {
			result = append(result, records[slot])
		}
	}
	return result
}

type deterministicRandom struct {
	state uint64
}

func newDeterministicRandom(seed uint64) *deterministicRandom {
	return &deterministicRandom{state: seed + 0x9e3779b97f4a7c15}
}

func (r *deterministicRandom) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	value := r.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (r *deterministicRandom) intn(limit int) int {
	if limit <= 0 {
		return 0
	}
	// 先对正 int limit 取模，结果严格小于 limit，因此转换回 int 不会溢出。
	//nolint:gosec // G115: 上述取模约束保证转换安全。
	return int(r.next() % uint64(limit))
}

func (r *deterministicRandom) shuffle(values []ScenarioType) {
	for index := len(values) - 1; index > 0; index-- {
		other := r.intn(index + 1)
		values[index], values[other] = values[other], values[index]
	}
}

// SortExpected 便于输出稳定 expected JSON，不改变生成记录顺序。
func SortExpected(expected *Expected) {
	if expected == nil {
		return
	}
	sort.Strings(expected.SourceEventIDs)
	sort.Slice(expected.Alerts, func(first, second int) bool {
		left := expected.Alerts[first]
		right := expected.Alerts[second]
		return fmt.Sprint(left.BKTenantID, left.Fingerprint, left.Severity, left.Status) <
			fmt.Sprint(right.BKTenantID, right.Fingerprint, right.Severity, right.Status)
	})
}
