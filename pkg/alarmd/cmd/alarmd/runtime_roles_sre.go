// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-redis/redis/v8"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/rollout"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

// channelSREOperations builds the SRE operations of a channel. The Pod
// provider needs nothing the deployment declares: in a cluster it reads any
// namespace a request names, this Pod's own by default, through the Pod's
// ServiceAccount; out of one its operations say they are unavailable and why.
// binding.SREAudit hears every execution and management call. Image changes
// keep their receipts in client, under the deployment's prefix, and are
// watched by this process until they end or it stops: stopping hands them
// back for another process to take over.
func channelSREOperations(cfg config.Config, binding ChannelBinding, client redis.UniversalClient) ([]obchannel.Operation, func(), error) {
	secrets := sreDeploymentSecrets(cfg)
	podReason := ""
	pods, err := pod.NewInCluster(pod.Options{ModuleLabel: cfg.SRE.ModuleLabel, LiteralSecrets: secrets})
	if err != nil {
		podReason = "pod_provider_unavailable: " + err.Error()
		pods = nil
	}
	querySource := cfg.PhaseTwo.Access.QuerySource
	if querySource == "" {
		querySource = config.DefaultQuerySource
	}
	queries, err := uq.NewWithQuerySource(cfg.SRE.WithUQEndpoint(cfg.PhaseTwo.Access.UQEndpoint).UQ.Egresses, nil, querySource)
	if err != nil {
		return nil, nil, err
	}
	// Every action's receipt goes to the deployment's shared list beside its
	// audit line, so sre.receipts on any replica reads them all.
	var receipts *obchannel.SREReceipts
	receiptsReason := "receipts_unavailable: the channel has no runtime store connection"
	if client != nil {
		receipts = obchannel.NewSREReceipts(client, sreKeyPrefix(cfg, binding)+"receipts", nil, binding.SREReceiptWritten)
		receiptsReason = ""
	}
	binding.SREAudit = auditWithReceipts(binding.SREAudit, receipts)
	rollouts, rolloutReason, stopRollouts := channelRollouts(cfg, binding, client)
	ops := obchannel.SREOperations(obchannel.SREOptions{Pods: pods, Queries: queries, Scrubber: redact.New(secrets...), PodReason: podReason,
		Rollouts: rollouts, RolloutReason: rolloutReason, Audit: binding.SREAudit, Receipts: receipts, ReceiptsReason: receiptsReason})
	return ops, func() { stopRollouts(); queries.Close() }, nil
}

// auditWithReceipts hears each action as audit does and records its
// receipt; a nil audit or store is skipped.
func auditWithReceipts(audit func(obchannel.SREAction), receipts *obchannel.SREReceipts) func(obchannel.SREAction) {
	return func(action obchannel.SREAction) {
		if audit != nil {
			audit(action)
		}
		receipts.Record(action)
	}
}

// sreKeyPrefix is where the SRE channel's shared state lives: image-change
// receipts and action receipts, one hash slot per environment.
func sreKeyPrefix(cfg config.Config, binding ChannelBinding) string {
	return cfg.Redis.StatePrefix + ".sre:{" + binding.EnvironmentID + "}:"
}

// channelRollouts starts the image-change driver: it takes over receipts
// another process left and watches those this one starts. Out of a cluster
// there is none, and the reason says why.
func channelRollouts(cfg config.Config, binding ChannelBinding, client redis.UniversalClient) (*rollout.Driver, string, func()) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, "rollout_unavailable: in-cluster configuration is unavailable", func() {}
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, "rollout_unavailable: " + err.Error(), func() {}
	}
	audit := binding.SREAudit
	driver := rollout.NewDriver(rollout.DriverOptions{
		Store:   rollout.NewStore(client, sreKeyPrefix(cfg, binding)),
		Cluster: rollout.NewKubernetesCluster(dynamicClient),
		Owner:   binding.InstanceID + "/" + binding.Control.Incarnation,
		Final:   binding.SRERolloutEnded,
		Revert: func(record rollout.Record) {
			if audit != nil {
				audit(obchannel.SREAction{Op: obchannel.OpRevert, Namespace: record.Namespace, Kind: "Deployment", Name: record.Name, UID: record.UID,
					Detail: "receipt " + record.ID + " container " + record.Container + ": " + record.Image + " -> " + record.Previous,
					Result: obchannel.SREResultDone, SessionID: record.SessionID, RequestID: record.RequestID})
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		driver.Run(ctx)
	}()
	if watched := binding.SRERolloutsWatched; watched != nil {
		go func() {
			ticker := time.NewTicker(rollout.DefaultScan)
			defer ticker.Stop()
			for {
				watched(driver.Driving())
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	return driver, "", func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

// sreAuditSink writes each execution or management call's sre_action line
// and counts it: who (session and request), what, where, and the result.
func sreAuditSink(logger *observability.Logger, recorder *metric.Recorder) func(obchannel.SREAction) {
	recorder.PresetSREActions(obchannel.SREActionOps, obchannel.SREActionResults)
	return func(action obchannel.SREAction) {
		recorder.RecordSREAction(action.Op, action.Result)
		attrs := []slog.Attr{slog.String("op", action.Op), slog.String("namespace", action.Namespace), slog.String("kind", action.Kind),
			slog.String("name", action.Name), slog.String("uid", action.UID), slog.String("detail", action.Detail), slog.String("code", action.Code),
			slog.String("session_id", action.SessionID), slog.String("request_id", action.RequestID)}
		duration := time.Duration(action.DurationMS) * time.Millisecond
		if action.Result == obchannel.SREResultDone {
			logger.Info("sre_action", action.Result, 0, duration, attrs...)
			return
		}
		logger.Warn("sre_action", action.Result, 0, duration, attrs...)
	}
}

// sreRolloutSink writes each image change this process ended as one
// sre_rollout line and counts it by final state.
func sreRolloutSink(logger *observability.Logger, recorder *metric.Recorder) func(rollout.Record) {
	recorder.PresetSRERollouts(rollout.FinalStates)
	return func(record rollout.Record) {
		recorder.RecordSRERollout(record.State)
		duration := time.Duration(record.EndedAt-record.StartedAt) * time.Millisecond
		attrs := []slog.Attr{slog.String("receipt_id", record.ID), slog.String("namespace", record.Namespace), slog.String("name", record.Name),
			slog.String("container", record.Container), slog.String("previous_image", record.Previous), slog.String("image", record.Image),
			slog.String("detail", record.Detail), slog.String("session_id", record.SessionID)}
		if record.State == rollout.StateComplete || record.State == rollout.StateUnchanged {
			logger.Info("sre_rollout", record.State, 0, duration, attrs...)
			return
		}
		logger.Warn("sre_rollout", record.State, 0, duration, attrs...)
	}
}

func sreDeploymentSecrets(cfg config.Config) []string {
	values := []string{cfg.CLI.AdminKey, cfg.PhaseTwo.Linkd.Password, cfg.Redis.Password, cfg.Redis.SentinelPassword}
	for _, connection := range []config.RedisConnectionConfig{cfg.RuntimeStoreRedis(), cfg.StrategySourceRedis(), cfg.CMDBCacheRedis(), cfg.Kafka.LegacyAdapter.ServiceRedis} {
		values = append(values, connection.Password, connection.SentinelPassword)
	}
	if connection, ok := cfg.TargetGroupRedis(); ok {
		values = append(values, connection.Password, connection.SentinelPassword)
	}
	if connection, ok := cfg.DynamicConfigRedis(); ok {
		values = append(values, connection.Password, connection.SentinelPassword)
	}
	// The pod cache's connection keeps its own credentials; only its
	// timeouts are taken from the runtime Redis.
	if cache := cfg.Kafka.LegacyAdapter.PodCache; cache != nil {
		values = append(values, cache.Connection.Password, cache.Connection.SentinelPassword)
	}
	return values
}
