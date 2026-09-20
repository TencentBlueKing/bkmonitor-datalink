// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package podterminating

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

type PodClient interface {
	List(context.Context, metav1.ListOptions) (*corev1.PodList, error)
	Watch(context.Context, metav1.ListOptions) (watch.Interface, error)
}

func observationForPod(pod *corev1.Pod) (Observation, bool) {
	if pod == nil || pod.DeletionTimestamp == nil {
		return Observation{}, false
	}
	startedAt := pod.DeletionTimestamp.Time
	if pod.DeletionGracePeriodSeconds != nil && *pod.DeletionGracePeriodSeconds > 0 {
		startedAt = startedAt.Add(-time.Duration(*pod.DeletionGracePeriodSeconds) * time.Second)
	}
	return Observation{
		UID: pod.UID,
		Dimension: Dimension{
			Namespace: pod.Namespace,
			Pod:       pod.Name,
			Node:      pod.Spec.NodeName,
		},
		DeletionStartedAt: startedAt,
	}, true
}

// ListSnapshot builds a consistent cluster snapshot without aggregating all
// Pods in memory. Each API page is projected immediately and only deleting Pods
// are retained. The returned resourceVersion is the snapshot boundary used to
// start the subsequent Watch.
func ListSnapshot(
	ctx context.Context,
	client PodClient,
	pageLimit int64,
	requestTimeout time.Duration,
) (map[types.UID]Observation, string, error) {
	if client == nil {
		return nil, "", fmt.Errorf("Pod client must not be nil")
	}
	if pageLimit <= 0 {
		return nil, "", fmt.Errorf("page limit must be positive")
	}
	if requestTimeout <= 0 {
		return nil, "", fmt.Errorf("request timeout must be positive")
	}

	observed := make(map[types.UID]Observation)
	continueToken := ""
	seenContinueTokens := make(map[string]struct{})
	resourceVersion := ""
	for {
		requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
		page, err := client.List(requestContext, metav1.ListOptions{
			Limit:    pageLimit,
			Continue: continueToken,
		})
		cancel()
		if err != nil {
			return nil, "", fmt.Errorf("list Pods page: %w", err)
		}
		if page.ResourceVersion == "" {
			return nil, "", fmt.Errorf("list Pods page returned an empty resourceVersion")
		}
		if resourceVersion == "" {
			resourceVersion = page.ResourceVersion
		} else if page.ResourceVersion != resourceVersion {
			return nil, "", fmt.Errorf(
				"list Pods pagination resourceVersion changed from %q to %q",
				resourceVersion,
				page.ResourceVersion,
			)
		}
		for index := range page.Items {
			observation, ok := observationForPod(&page.Items[index])
			if !ok {
				continue
			}
			if observation.UID == "" {
				return nil, "", fmt.Errorf(
					"deleting Pod %s/%s has an empty UID",
					observation.Dimension.Namespace,
					observation.Dimension.Pod,
				)
			}
			observed[observation.UID] = observation
		}
		if page.Continue == "" {
			return observed, resourceVersion, nil
		}
		if _, exists := seenContinueTokens[page.Continue]; exists {
			return nil, "", fmt.Errorf("list Pods returned repeated continue token %q", page.Continue)
		}
		seenContinueTokens[page.Continue] = struct{}{}
		continueToken = page.Continue
	}
}

func ApplyWatchEvent(observed map[types.UID]Observation, event watch.Event) (bool, error) {
	if observed == nil {
		return false, fmt.Errorf("observed Pod map must not be nil")
	}
	if event.Type == watch.Error {
		return false, apierrors.FromObject(event.Object)
	}
	pod, ok := event.Object.(*corev1.Pod)
	if !ok {
		return false, fmt.Errorf("Pod Watch event %q contains %T", event.Type, event.Object)
	}
	if pod.UID == "" {
		return false, fmt.Errorf("Pod Watch event %q for %s/%s has an empty UID", event.Type, pod.Namespace, pod.Name)
	}

	switch event.Type {
	case watch.Added, watch.Modified:
		observation, deleting := observationForPod(pod)
		if !deleting {
			if _, exists := observed[pod.UID]; exists {
				delete(observed, pod.UID)
				return true, nil
			}
			return false, nil
		}
		previous, exists := observed[pod.UID]
		if exists && previous == observation {
			return false, nil
		}
		observed[pod.UID] = observation
		return true, nil
	case watch.Deleted:
		if _, exists := observed[pod.UID]; !exists {
			return false, nil
		}
		delete(observed, pod.UID)
		return true, nil
	default:
		return false, fmt.Errorf("unsupported Pod Watch event type %q", event.Type)
	}
}
