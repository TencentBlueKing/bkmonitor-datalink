// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisstate

import (
	"context"
	"encoding/json"
	"errors"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/suppressioncleanup"
)

// ClearDependencyOwnersBatch 批量清理终态主登记，复用单项反向索引脚本。
func (s *Store) ClearDependencyOwnersBatch(ctx context.Context, alerts []domain.Alert) []error {
	pipe := s.client.Pipeline()
	commands := make([]*redis.Cmd, len(alerts))
	results := make([]error, len(alerts))
	// 首项 EVAL 在同一 pipeline 中加载脚本，后续复用缓存，减少批次正文体积。
	for i, a := range alerts {
		run := clearDependencyScript.EvalSha
		if i == 0 {
			run = clearDependencyScript.Eval
		}
		commands[i] = run(ctx, pipe, []string{s.dependencyOwner(a.BKTenantID, a.AlertID)}, s.base(a.BKTenantID)+":dependency:", a.AlertID)
	}
	_, _ = pipe.Exec(ctx)
	for i, c := range commands {
		n, err := c.Int()
		if err == nil && n < 0 {
			err = ErrState
		}
		results[i] = err
	}
	return results
}

// PublishShieldHintsBatch 批量发送终态主复查提示，仍由既有定时扫描补偿 Pub/Sub 丢失。
func (s *Store) PublishShieldHintsBatch(ctx context.Context, alerts []domain.Alert) ([]int64, []error) {
	pipe := s.client.Pipeline()
	commands := make([]*redis.IntCmd, len(alerts))
	counts := make([]int64, len(alerts))
	results := make([]error, len(alerts))
	for i, a := range alerts {
		raw, err := json.Marshal(ShieldHint{TenantID: a.BKTenantID, MainAlertID: a.AlertID})
		if err != nil {
			results[i] = err
			continue
		}
		commands[i] = pipe.Publish(ctx, s.shieldHintChannel(a.BKTenantID), raw)
	}
	_, _ = pipe.Exec(ctx)
	for i, c := range commands {
		if c != nil {
			counts[i], results[i] = c.Result()
		}
	}
	return counts, results
}

// ClearSuppressionBatch 将终态计数和聚合主清理放入同一 pipeline，保留逐项确认结果。
func (s *Store) ClearSuppressionBatch(ctx context.Context, causes []suppressioncleanup.Cause) ([]suppressioncleanup.Result, []error) {
	pipe := s.client.Pipeline()
	clips, aggregations := make([]*redis.Cmd, len(causes)), make([]*redis.Cmd, len(causes))
	results := make([]suppressioncleanup.Result, len(causes))
	errs := make([]error, len(causes))
	for i, c := range causes {
		base := s.base(c.TenantID)
		subject := digest("identity", c.SourceID, c.Fingerprint)
		runClip, runAggregation := clearClipScript.EvalSha, clearAggregationScript.EvalSha
		if i == 0 {
			runClip, runAggregation = clearClipScript.Eval, clearAggregationScript.Eval
		}
		clips[i] = runClip(ctx, pipe, []string{base + ":clip-index:" + subject, s.suppressionIndex(c.TenantID, "clip")}, base+":clip:"+subject+":", subject, c.AlertID)
		aggregations[i] = runAggregation(ctx, pipe, []string{base + ":aggregation-owner:" + digest(c.AlertID), s.suppressionIndex(c.TenantID, "aggregation")}, base+":aggregation:", c.AlertID, base+":aggregation-members:")
	}
	_, _ = pipe.Exec(ctx)
	for i, c := range causes {
		raw, err := clips[i].Slice()
		windows, clipErr := cleanupWindows(raw, err, "clip", digest("identity", c.SourceID, c.Fingerprint))
		results[i].Clip = suppressioncleanup.Outcome{State: "unavailable"}
		if clipErr == nil {
			results[i].Clip = suppressioncleanup.Confirmed(windows)
		}
		raw, err = aggregations[i].Slice()
		windows, aggregationErr := cleanupWindows(raw, err, "aggregation", "")
		results[i].Aggregation = suppressioncleanup.Outcome{State: "unavailable"}
		if aggregationErr == nil {
			results[i].Aggregation = suppressioncleanup.Confirmed(windows)
		}
		errs[i] = errors.Join(clipErr, aggregationErr)
	}
	return results, errs
}
