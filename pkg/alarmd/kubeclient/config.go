// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package kubeclient holds the API configuration every Kubernetes client
// alarmd builds starts from.
package kubeclient

import "k8s.io/client-go/rest"

// QPS and Burst replace client-go's defaults of 5 requests a second and a
// burst of 10. Those are a placeholder, not the API server's limit: one Pod
// discovery in a namespace of a hundred workloads reads two owners per Pod,
// and at 5 a second it outlasts its own deadline. The API server's priority
// and fairness is the real bound; this pace only keeps a runaway loop from
// reaching it unchecked, and an operator's reads never come near it.
const (
	QPS   = 200
	Burst = 400
)

// InCluster is the ServiceAccount's configuration at this pace.
func InCluster() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return WithRate(config), nil
}

// WithRate is a copy of config at this pace, unless config names a rate or a
// limiter of its own.
func WithRate(config *rest.Config) *rest.Config {
	config = rest.CopyConfig(config)
	if config.QPS == 0 && config.Burst == 0 && config.RateLimiter == nil {
		config.QPS, config.Burst = QPS, Burst
	}
	return config
}
