// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package podterminating

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

type Persistence interface {
	Save(context.Context, Snapshot) (int, error)
	ResolvePending(context.Context) (Snapshot, bool, error)
}

type RunnerOptions struct {
	PageLimit          int64
	RequestTimeout     time.Duration
	CheckpointInterval time.Duration
	RetryInterval      time.Duration
	// SetReady opens the startup readiness gate after the first successful
	// checkpoint. Runtime collection health is reported by State metrics and
	// must not close the metrics endpoint.
	SetReady func(bool)
}

type Runner struct {
	client  PodClient
	state   *State
	store   Persistence
	options RunnerOptions
	ready   bool
}

func NewRunner(client PodClient, state *State, store Persistence, options RunnerOptions) (*Runner, error) {
	if client == nil {
		return nil, fmt.Errorf("Pod client must not be nil")
	}
	if state == nil {
		return nil, fmt.Errorf("state must not be nil")
	}
	if store == nil {
		return nil, fmt.Errorf("state persistence must not be nil")
	}
	if options.PageLimit <= 0 {
		return nil, fmt.Errorf("page limit must be positive")
	}
	if options.RequestTimeout <= 0 {
		return nil, fmt.Errorf("request timeout must be positive")
	}
	if options.CheckpointInterval <= 0 {
		return nil, fmt.Errorf("checkpoint interval must be positive")
	}
	if options.RetryInterval <= 0 {
		options.RetryInterval = time.Second
	}
	return &Runner{
		client:  client,
		state:   state,
		store:   store,
		options: options,
	}, nil
}

func (r *Runner) markReady() {
	if r.ready {
		return
	}
	r.ready = true
	if r.options.SetReady != nil {
		r.options.SetReady(true)
	}
}

func (r *Runner) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		observed, resourceVersion, err := ListSnapshot(
			ctx,
			r.client,
			r.options.PageLimit,
			r.options.RequestTimeout,
		)
		if err != nil {
			r.state.MarkFailure()
			if !waitForRetry(ctx, r.options.RetryInterval) {
				return nil
			}
			continue
		}

		relist, err := r.consume(ctx, observed, resourceVersion)
		if err != nil {
			r.state.MarkFailure()
		}
		if ctx.Err() != nil {
			return nil
		}
		if !relist && err != nil {
			return err
		}
	}
}

func (r *Runner) consume(
	ctx context.Context,
	observed map[types.UID]Observation,
	resourceVersion string,
) (bool, error) {
	ticker := time.NewTicker(r.options.CheckpointInterval)
	defer ticker.Stop()

	dirty := true
	checkpoint := func(now time.Time) error {
		resolved, ok, err := r.store.ResolvePending(ctx)
		if err != nil {
			return err
		}
		if ok {
			raw, marshalErr := MarshalSnapshot(resolved)
			if marshalErr != nil {
				return marshalErr
			}
			if err := r.state.AcceptPersisted(resolved, observed, len(raw), now); err != nil {
				return err
			}
		}
		if err := r.state.Checkpoint(ctx, observed, now, r.store.Save); err != nil {
			return err
		}
		dirty = false
		r.markReady()
		return nil
	}
	_ = checkpoint(time.Now())

	timeoutSeconds := watchTimeoutSeconds(r.options.RequestTimeout)
	activityTimeout := r.options.RequestTimeout + r.options.RetryInterval
	for {
		watchContext, cancelWatch := context.WithTimeout(ctx, activityTimeout)
		watcher, err := r.client.Watch(watchContext, metav1.ListOptions{
			ResourceVersion:     resourceVersion,
			AllowWatchBookmarks: true,
			TimeoutSeconds:      &timeoutSeconds,
		})
		if err != nil {
			cancelWatch()
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
				return true, err
			}
			r.state.MarkFailure()
			if !waitForRetry(ctx, r.options.RetryInterval) {
				return false, nil
			}
			continue
		}
		if !dirty {
			r.state.MarkHealthy(time.Now())
		}

		activityTimer := time.NewTimer(activityTimeout)
		reconnect := false
		for !reconnect {
			select {
			case <-ctx.Done():
				stopTimer(activityTimer)
				watcher.Stop()
				cancelWatch()
				return false, nil
			case event, ok := <-watcher.ResultChan():
				if !ok {
					r.state.MarkFailure()
					reconnect = true
					continue
				}
				resetTimer(activityTimer, activityTimeout)
				nextResourceVersion, relist, eventErr := watchEventResourceVersion(event)
				if relist {
					stopTimer(activityTimer)
					watcher.Stop()
					cancelWatch()
					return true, eventErr
				}
				if eventErr != nil {
					r.state.MarkFailure()
					reconnect = true
					continue
				}
				resourceVersion = nextResourceVersion
				if event.Type == watch.Bookmark {
					if !dirty {
						r.state.MarkHealthy(time.Now())
					}
					continue
				}
				changed, applyErr := ApplyWatchEvent(observed, event)
				if applyErr != nil {
					stopTimer(activityTimer)
					watcher.Stop()
					cancelWatch()
					return true, applyErr
				}
				dirty = dirty || changed
				if !dirty {
					r.state.MarkHealthy(time.Now())
				}
			case now := <-ticker.C:
				if !dirty && !r.state.HasExpiredRecovery(now) {
					continue
				}
				dirty = true
				if checkpointErr := checkpoint(now); checkpointErr != nil {
					r.state.MarkFailure()
				}
			case <-activityTimer.C:
				r.state.MarkFailure()
				reconnect = true
			}
		}
		stopTimer(activityTimer)
		watcher.Stop()
		cancelWatch()
		if !waitForRetry(ctx, r.options.RetryInterval) {
			return false, nil
		}
	}
}

func watchEventResourceVersion(event watch.Event) (string, bool, error) {
	if event.Type == watch.Error {
		err := apierrors.FromObject(event.Object)
		return "", apierrors.IsResourceExpired(err) || apierrors.IsGone(err), err
	}
	object, ok := event.Object.(metav1.Object)
	if !ok {
		return "", true, fmt.Errorf("Pod Watch event %q contains %T without object metadata", event.Type, event.Object)
	}
	resourceVersion := object.GetResourceVersion()
	if resourceVersion == "" {
		return "", true, fmt.Errorf("Pod Watch event %q has an empty resourceVersion", event.Type)
	}
	return resourceVersion, false, nil
}

func waitForRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func watchTimeoutSeconds(timeout time.Duration) int64 {
	seconds := int64(timeout / time.Second)
	if time.Duration(seconds)*time.Second < timeout {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func resetTimer(timer *time.Timer, interval time.Duration) {
	stopTimer(timer)
	timer.Reset(interval)
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
