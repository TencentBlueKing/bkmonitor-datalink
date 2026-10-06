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
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	linkdconfig "linkd/internal/config"
)

const (
	// DefaultNewAlertsPerMinute 是模拟器默认每分钟新告警数。
	DefaultNewAlertsPerMinute = 20
	// DefaultCycleDuration 是模拟器默认调度周期。
	DefaultCycleDuration = 30 * time.Second
	// DefaultMeanLifetimeCycles 是新告警默认平均存活周期数。
	DefaultMeanLifetimeCycles = 4
	// DefaultMaxActiveAlerts 是进程内活动告警池默认硬上限。
	DefaultMaxActiveAlerts = 100_000

	maxNewAlertsPerMinute = 1_000_000
	maxActiveAlerts       = 1_000_000
	maxCycles             = 1_000_000
	minCycleDuration      = 10 * time.Millisecond
	maxCycleDuration      = 10 * time.Minute
	maxRunIDLength        = 64
)

var runIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Config 描述一次 eventgen 运行的速率、生命周期、容量和随机配置。
type Config struct {
	RunID              string
	TenantID           string
	NewAlertsPerMinute int
	CycleDuration      time.Duration
	MeanLifetimeCycles int
	DuplicatePercent   int
	Scenarios          []Scenario
	Seed               uint64
	MaxActiveAlerts    int
	Cycles             int
}

// Validate 校验模拟器的硬边界和必填运行参数。
func (c Config) Validate() error {
	if c.RunID == "" || len(c.RunID) > maxRunIDLength || !runIDPattern.MatchString(c.RunID) {
		return fmt.Errorf("run_id must contain 1 to %d letters, digits, underscores, or hyphens", maxRunIDLength)
	}
	if strings.TrimSpace(c.TenantID) != c.TenantID || c.TenantID == "" || len(c.TenantID) > 64 {
		return fmt.Errorf("tenant_id must contain 1 to 64 bytes without surrounding whitespace")
	}
	if c.NewAlertsPerMinute < 1 || c.NewAlertsPerMinute > maxNewAlertsPerMinute {
		return fmt.Errorf("new_alerts_per_minute must be between 1 and %d", maxNewAlertsPerMinute)
	}
	if c.CycleDuration < minCycleDuration || c.CycleDuration > maxCycleDuration {
		return fmt.Errorf("cycle_duration must be between %s and %s", minCycleDuration, maxCycleDuration)
	}
	if c.MeanLifetimeCycles < 1 || c.MeanLifetimeCycles > maxCycles {
		return fmt.Errorf("mean_lifetime_cycles must be between 1 and %d", maxCycles)
	}
	if c.DuplicatePercent < 0 || c.DuplicatePercent > 100 {
		return fmt.Errorf("duplicate_percent must be between 0 and 100")
	}
	if c.MaxActiveAlerts < 1 || c.MaxActiveAlerts > maxActiveAlerts {
		return fmt.Errorf("max_active_alerts must be between 1 and %d", maxActiveAlerts)
	}
	if c.Cycles < 0 || c.Cycles > maxCycles {
		return fmt.Errorf("cycles must be between 0 and %d", maxCycles)
	}
	if c.Seed == 0 {
		return fmt.Errorf("seed must be resolved to a non-zero value")
	}
	if len(c.Scenarios) == 0 {
		return fmt.Errorf("scenarios must contain at least one scenario")
	}
	seen := make(map[Scenario]struct{}, len(c.Scenarios))
	for _, scenario := range c.Scenarios {
		if !scenario.Valid() {
			return fmt.Errorf("unsupported scenario %q", scenario)
		}
		if _, exists := seen[scenario]; exists {
			return fmt.Errorf("scenario %q is duplicated", scenario)
		}
		seen[scenario] = struct{}{}
	}
	if uint64(c.NewAlertsPerMinute) > math.MaxUint64/uint64(c.CycleDuration) {
		return fmt.Errorf("new_alerts_per_minute and cycle_duration overflow the rate accumulator")
	}
	return nil
}

// ResolveSource 查找并校验 eventgen 可以写入的 EventSource，同时确定最终租户。
func ResolveSource(cfg linkdconfig.Config, eventSourceID, tenantID string) (linkdconfig.EventSource, string, error) {
	if eventSourceID == "" {
		return linkdconfig.EventSource{}, "", fmt.Errorf("event_source_id is required")
	}
	for _, candidate := range cfg.EventSources {
		if candidate.EventSourceID != eventSourceID {
			continue
		}
		source := candidate.WithDefaults()
		if !source.Enabled {
			return linkdconfig.EventSource{}, "", fmt.Errorf("event source %q is disabled", eventSourceID)
		}
		if source.Cleaner.Type != linkdconfig.CleanerTypeStandard {
			return linkdconfig.EventSource{}, "", fmt.Errorf(
				"event source %q cleaner must be %q", eventSourceID, linkdconfig.CleanerTypeStandard,
			)
		}
		if source.Storage.Type != linkdconfig.StorageTypeKafka {
			return linkdconfig.EventSource{}, "", fmt.Errorf(
				"event source %q storage must be %q", eventSourceID, linkdconfig.StorageTypeKafka,
			)
		}
		resolvedTenant, err := resolveTenant(source.RelatedTenantID, tenantID)
		if err != nil {
			return linkdconfig.EventSource{}, "", fmt.Errorf("event source %q: %w", eventSourceID, err)
		}
		if err := validateUniqueFingerprint(source); err != nil {
			return linkdconfig.EventSource{}, "", fmt.Errorf("event source %q: %w", eventSourceID, err)
		}
		return source, resolvedTenant, nil
	}
	return linkdconfig.EventSource{}, "", fmt.Errorf("event source %q was not found", eventSourceID)
}

func resolveTenant(relatedTenantID, requestedTenantID string) (string, error) {
	if relatedTenantID != "" {
		if requestedTenantID != "" && requestedTenantID != relatedTenantID {
			return "", fmt.Errorf(
				"tenant_id %q does not match related_tenant_id %q", requestedTenantID, relatedTenantID,
			)
		}
		return relatedTenantID, nil
	}
	if strings.TrimSpace(requestedTenantID) != requestedTenantID || requestedTenantID == "" || len(requestedTenantID) > 64 {
		return "", fmt.Errorf("tenant_id is required and must contain at most 64 bytes without surrounding whitespace")
	}
	return requestedTenantID, nil
}

func validateUniqueFingerprint(source linkdconfig.EventSource) error {
	unique := func(path string) bool {
		return path == "source_alert_id" || path == "subject_id" || path == "dimensions.generator_id"
	}
	switch source.FingerprintMode {
	case linkdconfig.FingerprintModeField:
		if unique(source.FingerprintField) {
			return nil
		}
	case linkdconfig.FingerprintModeFields:
		for _, path := range source.FingerprintFields {
			if unique(path) {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"fingerprint must include source_alert_id, subject_id, or dimensions.generator_id to guarantee uniqueness",
	)
}

// ResolveSeed 返回显式非零 seed，或使用系统随机源创建一个非零 seed。
func ResolveSeed(seed uint64) (uint64, error) {
	if seed != 0 {
		return seed, nil
	}
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return 0, fmt.Errorf("generate random seed: %w", err)
	}
	resolved := binary.BigEndian.Uint64(data[:])
	if resolved == 0 {
		resolved = 1
	}
	return resolved, nil
}

// NewRunID 创建仅用于本次独立模拟运行的随机命名空间。
func NewRunID() (string, error) {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}
