// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type temporaryLegacyDrainingCleanupRunner func(
	context.Context,
	config.Config,
	string,
	string,
	io.Writer,
) error

type temporaryLegacyProgressAdapter struct {
	store *progress.Store
}

func (adapter temporaryLegacyProgressAdapter) LoadProgress(
	ctx context.Context,
	identity execution.ProgressIdentity,
) (execution.ProgressLoadResult, error) {
	return adapter.store.LoadProgress(ctx, identity)
}

func (adapter temporaryLegacyProgressAdapter) LoadTemporaryLegacyDrainingProgress(
	ctx context.Context,
	identity execution.ProgressIdentity,
) (controlplane.TemporaryLegacyDrainingProgressFact, error) {
	result, err := adapter.store.LoadProgressForTemporaryLegacyDrainingCAS(ctx, identity)
	return controlplane.TemporaryLegacyDrainingProgressFact{
		RedisKey: result.RedisKey,
		Raw:      result.Raw,
		Load:     result.Load,
	}, err
}

func runTemporaryLegacyDrainingCleanup(
	ctx context.Context,
	cfg config.Config,
	requestPath string,
	expectedPlanDigest string,
	stdout io.Writer,
) (resultErr error) {
	if cfg.Input.Mode != config.InputModeGoAccess {
		return fmt.Errorf("temporary legacy Draining cleanup requires input mode %q", config.InputModeGoAccess)
	}
	request, err := loadTemporaryLegacyDrainingCleanupRequest(requestPath)
	if err != nil {
		return err
	}
	runtimeConnection := cfg.ResolvedRuntimeRedis()
	runtimeScopeDigest, err := temporaryLegacyDrainingRuntimeScopeDigest(runtimeConnection, cfg.Redis.StatePrefix)
	if err != nil {
		return err
	}
	runtimeClient, err := openProductionRedis(ctx, runtimeConnection)
	if err != nil {
		return fmt.Errorf("open temporary cleanup runtime Redis: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, runtimeClient.Close()) }()

	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		return err
	}
	stateSemantics, err := state.RuntimeStateSemantics()
	if err != nil {
		return err
	}
	strategySemantics := strategy.StateSemantics{
		StateSchemaVersion:          stateSemantics.StateSchemaVersion,
		CodecSemanticsVersion:       stateSemantics.CodecSemanticsVersion,
		IdentitySchemaDigest:        stateSemantics.IdentitySchemaDigest,
		SourceTimeSemanticsVersion:  stateSemantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: stateSemantics.HistoryCellSemanticsVersion,
	}
	repository, err := controlplane.NewRedisCatalogRepository(
		runtimeClient,
		productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"),
		cfg.PhaseTwo.Control.CatalogTTL.Duration(),
	)
	if err != nil {
		return err
	}
	timelineCache := config.DeriveControlTimelineCache(config.DetectCapacityInputs())
	if err := repository.ConfigureControlTimelineCache(timelineCache.MaxEntries, timelineCache.MaxBytes); err != nil {
		return err
	}
	catalog, err := controlplane.NewRedisCatalogRuntime(
		repository,
		compiler,
		strategySemantics,
		cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(),
	)
	if err != nil {
		return err
	}
	ownershipStore, err := ownership.NewRedisStoreWithClient(
		runtimeClient,
		productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"),
	)
	if err != nil {
		return err
	}
	progressStore, err := progress.NewStore(progress.StoreOptions{
		Prefix:  productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"),
		Control: ownershipStore,
		Slots:   catalog,
		Now:     time.Now,
	})
	if err != nil {
		return err
	}
	cleanup, err := controlplane.NewTemporaryLegacyDrainingCleanup(
		repository,
		compiler,
		strategySemantics,
		temporaryLegacyProgressAdapter{store: progressStore},
		runtimeScopeDigest,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if expectedPlanDigest == "" {
		plan, err := cleanup.DryRun(ctx, request)
		if err != nil {
			return err
		}
		return encoder.Encode(plan)
	}
	result, applyErr := cleanup.Apply(ctx, request, expectedPlanDigest)
	if err := encoder.Encode(result); err != nil {
		return errors.Join(applyErr, err)
	}
	return applyErr
}

func temporaryLegacyDrainingRuntimeScopeDigest(
	connection config.RedisConnectionConfig,
	statePrefix string,
) (string, error) {
	type runtimeScope struct {
		Mode              string   `json:"mode"`
		Address           string   `json:"address,omitempty"`
		SentinelAddresses []string `json:"sentinel_addresses,omitempty"`
		MasterName        string   `json:"master_name,omitempty"`
		DB                int      `json:"db"`
		StatePrefix       string   `json:"state_prefix"`
	}
	if connection.DB < 0 || statePrefix == "" || strings.TrimSpace(statePrefix) != statePrefix ||
		strings.ContainsAny(statePrefix, "{} \t\r\n") {
		return "", errors.New("temporary legacy Draining cleanup runtime scope is invalid")
	}
	scope := runtimeScope{Mode: connection.Mode, DB: connection.DB, StatePrefix: statePrefix}
	switch connection.Mode {
	case config.RedisModeStandalone:
		if connection.Address == "" {
			return "", errors.New("temporary legacy Draining cleanup standalone Redis address is required")
		}
		scope.Address = connection.Address
	case config.RedisModeSentinel:
		if len(connection.SentinelAddress) == 0 || connection.MasterName == "" {
			return "", errors.New("temporary legacy Draining cleanup Sentinel Redis scope is incomplete")
		}
		scope.SentinelAddresses = append([]string(nil), connection.SentinelAddress...)
		sort.Strings(scope.SentinelAddresses)
		scope.MasterName = connection.MasterName
	default:
		return "", errors.New("temporary legacy Draining cleanup Redis mode is invalid")
	}
	payload, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func loadTemporaryLegacyDrainingCleanupRequest(
	path string,
) (controlplane.TemporaryLegacyDrainingCleanupRequest, error) {
	file, err := os.Open(path)
	if err != nil {
		return controlplane.TemporaryLegacyDrainingCleanupRequest{}, fmt.Errorf("open temporary cleanup request: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var request controlplane.TemporaryLegacyDrainingCleanupRequest
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("decode temporary cleanup request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return request, fmt.Errorf("decode temporary cleanup request trailing content: %w", err)
	}
	return request, nil
}
