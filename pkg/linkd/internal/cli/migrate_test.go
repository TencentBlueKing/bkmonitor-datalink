// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
)

func TestMigrateCommand(t *testing.T) {
	t.Parallel()
	for _, wantFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[wantFailure], func(t *testing.T) {
			sentinel := errors.New("initialization failed")
			called := false
			root := NewRootCommand("test", "test-commit", Dependencies{MigrationRunner: func(ctx context.Context, _ config.Config) error {
				called = true
				deadline, exists := ctx.Deadline()
				if !exists || time.Until(deadline) > 4*time.Minute {
					t.Fatal("missing bounded deadline")
				}
				if wantFailure {
					return sentinel
				}
				return nil
			}})
			out := &bytes.Buffer{}
			root.SetOut(out)
			root.SetArgs([]string{"storage", "migrate", "--config", writeCLIConfig(t, "logging: {level: info, format: json}")})
			err := root.ExecuteContext(t.Context())
			if !called {
				t.Fatal("initializer not called")
			}
			if wantFailure {
				if !errors.Is(err, sentinel) || out.Len() != 0 {
					t.Fatalf("error=%v output=%s", err, out.String())
				}
			} else if err != nil || !strings.Contains(out.String(), "initialization completed") {
				t.Fatalf("error=%v output=%s", err, out.String())
			}
		})
	}
}

func TestMigrateCommandRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--timeout", "0s"}, {"--timeout", "31m"}, {"unexpected"},
		{"--config", "/missing/linkd.yaml"},
	} {
		root := NewRootCommand("test", "test-commit", Dependencies{MigrationRunner: func(context.Context, config.Config) error {
			t.Fatal("invalid input reached initializer")
			return nil
		}})
		root.SetArgs(append([]string{"storage", "migrate"}, args...))
		if err := root.ExecuteContext(t.Context()); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestMigrateCommandPropagatesTimeout(t *testing.T) {
	t.Parallel()
	root := NewRootCommand("test", "test-commit", Dependencies{MigrationRunner: func(ctx context.Context, _ config.Config) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	root.SetArgs([]string{"storage", "migrate", "--timeout", "1s", "--config", writeCLIConfig(t, "logging: {}")})
	if err := root.ExecuteContext(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}
