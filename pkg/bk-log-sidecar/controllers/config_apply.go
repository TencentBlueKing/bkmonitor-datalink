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
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

const generatedConfigSuffix = ".conf"

// desiredConfig keeps the rendered bytes together with their source object.
// Build completes this entire set before Apply is allowed to mutate disk state.
type desiredConfig struct {
	logConfig define.LogConfigType
	content   []byte
}

type desiredConfigSet map[string]desiredConfig

type stagedConfig struct {
	finalPath string
	tempPath  string
}

type backedUpConfig struct {
	originalPath string
	backupPath   string
}

func renderDesiredConfigs(logConfigs []define.LogConfigType) (desiredConfigSet, error) {
	desired := make(desiredConfigSet, len(logConfigs))
	for _, logConfig := range logConfigs {
		content, err := logConfig.Config()
		if err != nil {
			return nil, fmt.Errorf("render config %s: %w", logConfig.ConfigName(), err)
		}
		if len(content) == 0 {
			return nil, fmt.Errorf("render config %s: empty content", logConfig.ConfigName())
		}
		desired[logConfig.ConfigName()] = desiredConfig{
			logConfig: logConfig,
			content:   content,
		}
	}
	return desired, nil
}

// applyDesiredConfigs serializes every config-file transaction. Runtime events
// and Kubernetes reconciles can arrive concurrently, but neither may overwrite
// a newer desired snapshot while another transaction is being applied.
func (s *BkLogSidecar) applyDesiredConfigs(desired desiredConfigSet) error {
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	return s.applyDesiredConfigsLocked(desired, true, nil)
}

// applyDesiredConfigsLocked applies a fully rendered snapshot, updates the
// in-memory cache only after the disk transaction succeeds, and reloads only
// when disk state changed or an earlier reload is still pending.
func (s *BkLogSidecar) applyDesiredConfigsLocked(
	desired desiredConfigSet,
	pruneObsolete bool,
	explicitDeletes map[string]struct{},
) error {
	changed, err := s.applyDesiredConfigFiles(desired, pruneObsolete, explicitDeletes)
	if err != nil {
		return err
	}

	s.replaceActualConfigCache(desired)
	if changed {
		s.reloadPending = true
	}
	if !s.reloadPending {
		return nil
	}

	if err := s.reloadAgent(); err != nil {
		// Keep reloadPending set. A controller-runtime retry with identical file
		// content must still retry the signal instead of taking the no-diff path.
		return fmt.Errorf("reload agent for desired config: %w", err)
	}
	s.reloadPending = false
	return nil
}

func (s *BkLogSidecar) replaceActualConfigCache(desired desiredConfigSet) {
	s.actualBkLogConfigCache.Range(func(key, _ interface{}) bool {
		s.actualBkLogConfigCache.Delete(key)
		return true
	})
	for name, generated := range desired {
		s.actualBkLogConfigCache.Store(name, generated.logConfig)
	}
}

func (s *BkLogSidecar) desiredConfigsFromCacheLocked() (desiredConfigSet, error) {
	logConfigs := make([]define.LogConfigType, 0)
	s.actualBkLogConfigCache.Range(func(_, value interface{}) bool {
		logConfigs = append(logConfigs, value.(define.LogConfigType))
		return true
	})
	return renderDesiredConfigs(logConfigs)
}

func (s *BkLogSidecar) upsertActualConfigs(logConfigs []define.LogConfigType) error {
	additional, err := renderDesiredConfigs(logConfigs)
	if err != nil {
		return err
	}

	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	desired, err := s.desiredConfigsFromCacheLocked()
	if err != nil {
		return fmt.Errorf("render current config snapshot: %w", err)
	}
	for name, generated := range additional {
		desired[name] = generated
	}
	// A CREATE event is incremental. It must not prune files absent from the
	// still-warming in-memory cache during startup.
	return s.applyDesiredConfigsLocked(desired, false, nil)
}

// deleteContainerConfig removes a container from a cloned desired snapshot.
// The live cache and files remain untouched if rendering or Apply fails.
func (s *BkLogSidecar) deleteContainerConfig(container *define.Container) error {
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	desired, err := s.desiredConfigsFromCacheLocked()
	if err != nil {
		return fmt.Errorf("render current config snapshot: %w", err)
	}
	explicitDeletes := make(map[string]struct{})
	for name := range desired {
		if strings.HasPrefix(name, container.ID) {
			delete(desired, name)
			explicitDeletes[name] = struct{}{}
		}
	}
	return s.applyDesiredConfigsLocked(desired, false, explicitDeletes)
}

func (s *BkLogSidecar) applyDesiredConfigFiles(
	desired desiredConfigSet,
	pruneObsolete bool,
	explicitDeletes map[string]struct{},
) (bool, error) {
	current, err := readCurrentConfigFiles(config.BkunifylogbeatConfig)
	if err != nil {
		return false, err
	}

	changedNames := make([]string, 0)
	for name, generated := range desired {
		content, ok := current[name]
		if !ok || !bytes.Equal(content, generated.content) {
			changedNames = append(changedNames, name)
		}
	}
	obsoleteNames := make([]string, 0)
	if pruneObsolete {
		for name := range current {
			if _, ok := desired[name]; !ok {
				obsoleteNames = append(obsoleteNames, name)
			}
		}
	} else {
		for name := range explicitDeletes {
			if _, ok := current[name]; ok {
				obsoleteNames = append(obsoleteNames, name)
			}
		}
	}
	if len(changedNames) == 0 && len(obsoleteNames) == 0 {
		return false, nil
	}
	sort.Strings(changedNames)
	sort.Strings(obsoleteNames)

	staged := make(map[string]stagedConfig, len(changedNames))
	defer cleanupStagedConfigs(staged)
	for _, name := range changedNames {
		stagedConfig, err := stageConfigFile(config.BkunifylogbeatConfig, name, desired[name].content)
		if err != nil {
			return false, err
		}
		staged[name] = stagedConfig
	}

	backupNames := make([]string, 0, len(changedNames)+len(obsoleteNames))
	for _, name := range changedNames {
		if _, ok := current[name]; ok {
			backupNames = append(backupNames, name)
		}
	}
	backupNames = append(backupNames, obsoleteNames...)
	sort.Strings(backupNames)

	backups := make([]backedUpConfig, 0, len(backupNames))
	for _, name := range backupNames {
		originalPath := configFilePath(config.BkunifylogbeatConfig, name)
		backupPath, err := reserveBackupPath(config.BkunifylogbeatConfig)
		if err != nil {
			rollbackErr := rollbackConfigTransaction(nil, backups)
			return false, errors.Join(err, rollbackErr)
		}
		if err := os.Rename(originalPath, backupPath); err != nil {
			rollbackErr := rollbackConfigTransaction(nil, backups)
			return false, errors.Join(fmt.Errorf("backup config file %s: %w", originalPath, err), rollbackErr)
		}
		backups = append(backups, backedUpConfig{originalPath: originalPath, backupPath: backupPath})
	}

	installed := make([]string, 0, len(changedNames))
	for _, name := range changedNames {
		candidate := staged[name]
		if err := os.Rename(candidate.tempPath, candidate.finalPath); err != nil {
			rollbackErr := rollbackConfigTransaction(installed, backups)
			return false, errors.Join(fmt.Errorf("install config file %s: %w", candidate.finalPath, err), rollbackErr)
		}
		installed = append(installed, candidate.finalPath)
	}

	// Backup cleanup is best effort after all desired files are live. Returning
	// an error here would suppress reload even though the new snapshot already
	// succeeded, so cleanup residue is logged and ignored.
	for _, backup := range backups {
		if err := os.Remove(backup.backupPath); err != nil && !os.IsNotExist(err) {
			s.log.Error(err, "remove config transaction backup failed", "path", backup.backupPath)
		}
	}
	return true, nil
}

func readCurrentConfigFiles(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read config directory %s: %w", dir, err)
	}
	current := make(map[string][]byte)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), generatedConfigSuffix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read generated config %s: %w", path, err)
		}
		name := strings.TrimSuffix(entry.Name(), generatedConfigSuffix)
		current[name] = content
	}
	return current, nil
}

func stageConfigFile(dir, name string, content []byte) (stagedConfig, error) {
	file, err := os.CreateTemp(dir, ".bklog-sidecar-stage-*")
	if err != nil {
		return stagedConfig{}, fmt.Errorf("create staged config for %s: %w", name, err)
	}
	tempPath := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}
	if err := file.Chmod(0o644); err != nil {
		cleanup()
		return stagedConfig{}, fmt.Errorf("chmod staged config for %s: %w", name, err)
	}
	written, err := file.Write(content)
	if err != nil {
		cleanup()
		return stagedConfig{}, fmt.Errorf("write staged config for %s: %w", name, err)
	}
	if written != len(content) {
		cleanup()
		return stagedConfig{}, fmt.Errorf("write staged config for %s: wrote %d of %d bytes", name, written, len(content))
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return stagedConfig{}, fmt.Errorf("sync staged config for %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return stagedConfig{}, fmt.Errorf("close staged config for %s: %w", name, err)
	}
	return stagedConfig{
		finalPath: configFilePath(dir, name),
		tempPath:  tempPath,
	}, nil
}

func reserveBackupPath(dir string) (string, error) {
	file, err := os.CreateTemp(dir, ".bklog-sidecar-backup-*")
	if err != nil {
		return "", fmt.Errorf("reserve config backup path: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close reserved config backup %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("release reserved config backup %s: %w", path, err)
	}
	return path, nil
}

func rollbackConfigTransaction(installed []string, backups []backedUpConfig) error {
	rollbackErrors := make([]error, 0)
	for i := len(installed) - 1; i >= 0; i-- {
		if err := os.Remove(installed[i]); err != nil && !os.IsNotExist(err) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove installed config %s during rollback: %w", installed[i], err))
		}
	}
	for i := len(backups) - 1; i >= 0; i-- {
		backup := backups[i]
		if err := os.Rename(backup.backupPath, backup.originalPath); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore config %s during rollback: %w", backup.originalPath, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func cleanupStagedConfigs(staged map[string]stagedConfig) {
	for _, candidate := range staged {
		if err := os.Remove(candidate.tempPath); err != nil && !os.IsNotExist(err) {
			// Staging files do not use the .conf suffix, so a cleanup failure cannot
			// make bkunifylogbeat load an incomplete candidate.
			continue
		}
	}
}

func configFilePath(dir, name string) string {
	return filepath.Join(dir, name+generatedConfigSuffix)
}
