// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const ServiceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// Let promhttp write its timeout response before the server write deadline.
const httpWriteTimeoutGrace = time.Second

type ReporterOptions struct {
	ListenAddress      string
	Namespace          string
	StateConfigMapName string
	RefreshInterval    time.Duration
	PageLimit          int64
	RequestTimeout     time.Duration
	RecoveryHold       time.Duration
	StaleAfter         time.Duration
	StateMaxBytes      int
}

func (o ReporterOptions) Validate() error {
	_, port, err := net.SplitHostPort(o.ListenAddress)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", o.ListenAddress, err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid listen address port %q", port)
	}
	if errors := validation.IsDNS1123Label(o.Namespace); len(errors) > 0 {
		return fmt.Errorf("invalid namespace %q: %s", o.Namespace, strings.Join(errors, ", "))
	}
	if errors := validation.IsDNS1123Subdomain(o.StateConfigMapName); len(errors) > 0 {
		return fmt.Errorf("invalid state ConfigMap name %q: %s", o.StateConfigMapName, strings.Join(errors, ", "))
	}
	if o.RefreshInterval <= 0 {
		return fmt.Errorf("refresh interval must be positive")
	}
	if o.PageLimit <= 0 {
		return fmt.Errorf("page limit must be positive")
	}
	if o.RequestTimeout <= 0 {
		return fmt.Errorf("request timeout must be positive")
	}
	if o.RecoveryHold <= 0 {
		return fmt.Errorf("recovery hold must be positive")
	}
	if o.StaleAfter <= 0 {
		return fmt.Errorf("stale after must be positive")
	}
	if o.StateMaxBytes <= 0 || o.StateMaxBytes > HardMaxStateBytes {
		return fmt.Errorf("state max bytes %d exceeds hard limit %d or is not positive", o.StateMaxBytes, HardMaxStateBytes)
	}
	return nil
}

func ResolveNamespace(explicit, namespacePath string) (string, error) {
	if namespace := strings.TrimSpace(explicit); namespace != "" {
		return namespace, nil
	}
	raw, err := os.ReadFile(namespacePath)
	if err != nil {
		return "", fmt.Errorf("read ServiceAccount namespace: %w", err)
	}
	namespace := strings.TrimSpace(string(raw))
	if namespace == "" {
		return "", fmt.Errorf("ServiceAccount namespace file is empty")
	}
	return namespace, nil
}

func RunInCluster(ctx context.Context, options ReporterOptions) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("create in-cluster Kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	return Run(ctx, client, options)
}

func Run(ctx context.Context, client kubernetes.Interface, options ReporterOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", options.ListenAddress, err)
	}
	return runOnListener(ctx, client, options, listener)
}

func runOnListener(
	ctx context.Context,
	client kubernetes.Interface,
	options ReporterOptions,
	listener net.Listener,
) error {
	if err := options.Validate(); err != nil {
		_ = listener.Close()
		return err
	}
	store, err := NewStateStore(
		client,
		options.Namespace,
		options.StateConfigMapName,
		options.RequestTimeout,
		options.StateMaxBytes,
	)
	if err != nil {
		_ = listener.Close()
		return err
	}
	state := NewState(options.RecoveryHold, options.StaleAfter)
	server := newHTTPServer(state, options)

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	go refreshLoop(runContext, state, store, client, options)

	serverResult := make(chan error, 1)
	go func() {
		serverResult <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), options.RequestTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shut down reporter HTTP server: %w", err)
		}
		err := <-serverResult
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-serverResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve reporter HTTP: %w", err)
	}
}

func newHTTPServer(state *State, options ReporterOptions) *http.Server {
	return &http.Server{
		Handler:           NewHTTPHandler(state, time.Now, options.RequestTimeout),
		ReadHeaderTimeout: options.RequestTimeout,
		WriteTimeout:      options.RequestTimeout + httpWriteTimeoutGrace,
		IdleTimeout:       options.RequestTimeout,
	}
}

func refreshLoop(
	ctx context.Context,
	state *State,
	store *StateStore,
	client kubernetes.Interface,
	options ReporterOptions,
) {
	loaded := false
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if !loaded {
				snapshot, stateBytes, err := store.Load(ctx)
				if err != nil {
					state.ObserveKubernetesAPIError(err)
					state.MarkFailure()
					logger.Errorf("load pod terminating reporter state failed: %v", err)
					timer.Reset(options.RefreshInterval)
					continue
				}
				if err := state.Restore(snapshot, stateBytes, time.Now()); err != nil {
					state.MarkFailure()
					logger.Errorf("restore pod terminating reporter state failed: %v", err)
					timer.Reset(options.RefreshInterval)
					continue
				}
				loaded = true
			}
			if err := RefreshOnce(
				ctx,
				state,
				store,
				client,
				options.PageLimit,
				options.RequestTimeout,
				time.Now(),
			); err != nil {
				logger.Errorf("refresh pod terminating reporter failed: %v", err)
			}
			timer.Reset(options.RefreshInterval)
		}
	}
}
