// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestRenderDesiredConfigsReturnsSerializationFailure(t *testing.T) {
	renderErr := errors.New("invalid extOptions")

	_, err := renderDesiredConfigs([]define.LogConfigType{
		&stubLogConfig{name: "config-1", err: renderErr},
	})

	assert.ErrorIs(t, err, renderErr)
}

func TestRenderDesiredConfigsReturnsExtOptionsSerializationFailure(t *testing.T) {
	logConfig := &define.NodeLogConfig{
		BkLogConfig: bluekingv1alpha1.BkLogConfig{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "invalid-options"},
			Spec: bluekingv1alpha1.BkLogConfigSpec{
				LogConfigType: config.NodeLogConfig,
				ExtOptions: map[string]k8sruntime.RawExtension{
					"tail_files": {Raw: []byte("{")},
				},
			},
		},
		Node: &corev1.Node{},
	}

	_, err := renderDesiredConfigs([]define.LogConfigType{logConfig})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "marshal node log config")
}

func TestApplyDesiredConfigsSkipsReloadWhenFilesMatch(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	logConfig := &stubLogConfig{name: "config-1", content: []byte("same config")}
	desired, err := renderDesiredConfigs([]define.LogConfigType{logConfig})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(config.BkunifylogbeatConfig, "config-1.conf"), logConfig.content, 0o600))
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	err = sidecar.applyDesiredConfigs(desired)

	assert.NoError(t, err)
	assert.Equal(t, int32(0), reloadCalls.Load())
	_, ok := sidecar.actualBkLogConfigCache.Load(logConfig.ConfigName())
	assert.True(t, ok)
}

func TestApplyDesiredConfigsRetriesPendingReloadWithoutFileChanges(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	logConfig := &stubLogConfig{name: "config-1", content: []byte("new config")}
	desired, err := renderDesiredConfigs([]define.LogConfigType{logConfig})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(config.BkunifylogbeatConfig, "config-1.conf"), []byte("old config"), 0o600))
	reloadErr := errors.New("reload unavailable")
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		if reloadCalls.Add(1) == 1 {
			return reloadErr
		}
		return nil
	}

	firstErr := sidecar.applyDesiredConfigs(desired)
	secondErr := sidecar.applyDesiredConfigs(desired)

	assert.ErrorIs(t, firstErr, reloadErr)
	assert.NoError(t, secondErr)
	assert.Equal(t, int32(2), reloadCalls.Load())
	content, readErr := os.ReadFile(filepath.Join(config.BkunifylogbeatConfig, "config-1.conf"))
	require.NoError(t, readErr)
	assert.Equal(t, logConfig.content, content)
}

func TestApplyDesiredConfigsDeletesObsoleteFileAndReloads(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	staleConfig := &stubLogConfig{name: "stale-config", content: []byte("stale config")}
	stalePath := filepath.Join(config.BkunifylogbeatConfig, staleConfig.ConfigName()+generatedConfigSuffix)
	require.NoError(t, os.WriteFile(stalePath, staleConfig.content, 0o600))
	sidecar.actualBkLogConfigCache.Store(staleConfig.ConfigName(), staleConfig)
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	err := sidecar.applyDesiredConfigs(desiredConfigSet{})

	assert.NoError(t, err)
	assert.Equal(t, int32(1), reloadCalls.Load())
	_, statErr := os.Stat(stalePath)
	assert.True(t, os.IsNotExist(statErr))
	_, ok := sidecar.actualBkLogConfigCache.Load(staleConfig.ConfigName())
	assert.False(t, ok)
}

func TestUpsertActualConfigsDoesNotPruneFilesMissingFromWarmCache(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	previousPath := filepath.Join(config.BkunifylogbeatConfig, "previous-config.conf")
	require.NoError(t, os.WriteFile(previousPath, []byte("previous config"), 0o600))
	newConfig := &stubLogConfig{name: "new-config", content: []byte("new config")}
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	err := sidecar.upsertActualConfigs([]define.LogConfigType{newConfig})

	assert.NoError(t, err)
	assert.Equal(t, int32(1), reloadCalls.Load())
	previousContent, readErr := os.ReadFile(previousPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("previous config"), previousContent)
	newContent, readErr := os.ReadFile(filepath.Join(config.BkunifylogbeatConfig, "new-config.conf"))
	require.NoError(t, readErr)
	assert.Equal(t, newConfig.content, newContent)
}

func TestDeleteContainerConfigOnlyRemovesTargetContainerFiles(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	target := &stubLogConfig{name: "container-1_std_default_config", content: []byte("target config")}
	sidecar.actualBkLogConfigCache.Store(target.ConfigName(), target)
	targetPath := filepath.Join(config.BkunifylogbeatConfig, target.ConfigName()+generatedConfigSuffix)
	require.NoError(t, os.WriteFile(targetPath, target.content, 0o600))
	otherPath := filepath.Join(config.BkunifylogbeatConfig, "container-2_std_default_config.conf")
	require.NoError(t, os.WriteFile(otherPath, []byte("other config"), 0o600))
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	err := sidecar.deleteContainerConfig(&define.Container{ID: "container-1"})

	assert.NoError(t, err)
	assert.Equal(t, int32(1), reloadCalls.Load())
	_, statErr := os.Stat(targetPath)
	assert.True(t, os.IsNotExist(statErr))
	otherContent, readErr := os.ReadFile(otherPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("other config"), otherContent)
}

func TestApplyDesiredConfigsRollsBackPartialReplacement(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	existing := &stubLogConfig{name: "a-existing", content: []byte("old config")}
	sidecar.actualBkLogConfigCache.Store(existing.ConfigName(), existing)
	existingPath := filepath.Join(config.BkunifylogbeatConfig, "a-existing.conf")
	require.NoError(t, os.WriteFile(existingPath, existing.content, 0o600))
	// A directory at the destination makes the second rename fail after the
	// first file was installed, which exercises the transaction rollback path.
	require.NoError(t, os.Mkdir(filepath.Join(config.BkunifylogbeatConfig, "z-blocked.conf"), 0o700))
	desired, err := renderDesiredConfigs([]define.LogConfigType{
		&stubLogConfig{name: "a-existing", content: []byte("new config")},
		&stubLogConfig{name: "z-blocked", content: []byte("blocked config")},
	})
	require.NoError(t, err)
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	err = sidecar.applyDesiredConfigs(desired)

	assert.Error(t, err)
	content, readErr := os.ReadFile(existingPath)
	require.NoError(t, readErr)
	assert.Equal(t, existing.content, content)
	assert.Equal(t, int32(0), reloadCalls.Load())
	cached, ok := sidecar.actualBkLogConfigCache.Load(existing.ConfigName())
	require.True(t, ok)
	assert.Same(t, existing, cached)
}
