// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package platformsettings is alarmd's copy of the platform settings it
// evaluates by: the host states that exclude a host from monitoring, whether
// the computing platform is wired in, the CMDB-level tables that are, and
// the file system types the disk metrics ignore. Their owner is the base
// platform, which keeps them in its database and distributes them through
// Redis under its dynamic configuration protocol (v1); an operator changes
// them on a page, and every consumer is expected to follow within minutes.
//
// alarmd was carrying them as a transcription in its own configuration,
// which followed nothing. This package reads the distribution, keeps the
// last good reading, and answers with an effective value resolved through
// the same layers the protocol prescribes for every consumer.
package platformsettings

import (
	"errors"
	"strings"
)

// ProtocolVersion is the version of the distribution protocol this package
// reads. It is pinned rather than negotiated: a key layout that changes
// without a new version is a fault at the publisher, not something to adapt
// to.
const ProtocolVersion = 1

// DefaultKeyPrefix is the base platform's default common.redis_key_prefix.
// The deployment's actual prefix is rendered into alarmd's configuration
// from the platform's own setting; this is only what a bare process uses.
const DefaultKeyPrefix = "bk_monitor_base:"

// Tenant is the tenant whose settings alarmd reads. The protocol
// distinguishes per-tenant overrides from the shared baseline; every
// setting here is read at the baseline until a tenant-scoped evaluation
// exists to ask for another.
const Tenant = "system"

const (
	dynamicConfigSegment = "dynamic_config:"
	revisionSuffix       = "revision"
)

// ValidateKeyPrefix applies the protocol's rule for the prefix: used
// verbatim, with the trailing colon supplied by the setting, and never
// containing the hash tag characters the tenant segment reserves.
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" {
		return errors.New("alarmd platformsettings: a redis key prefix is required")
	}
	if strings.ContainsAny(prefix, "{} \t\r\n") {
		return errors.New("alarmd platformsettings: the redis key prefix must not contain braces or whitespace")
	}
	return nil
}

// RevisionKey is the protocol's global version key.
func RevisionKey(prefix string) string {
	return prefix + dynamicConfigSegment + revisionSuffix
}

// ConfigKey is the protocol's key for one setting of one tenant.
func ConfigKey(prefix, tenant, dbKey string) string {
	return prefix + dynamicConfigSegment + "{" + encodeTenant(tenant) + "}:" + dbKey
}

// encodeTenant is urllib.parse.quote(tenant, safe=""): every byte outside
// the unreserved set is percent-encoded, upper-case hex.
func encodeTenant(tenant string) string {
	const upperHex = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(tenant); index++ {
		character := tenant[index]
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '-', character == '_', character == '.', character == '~':
			builder.WriteByte(character)
		default:
			builder.WriteByte('%')
			builder.WriteByte(upperHex[character>>4])
			builder.WriteByte(upperHex[character&0x0f])
		}
	}
	return builder.String()
}

// Field names one setting alarmd reads. Closed set: the fields are the
// package's contract with the publisher, and a field that is not here is
// not read.
type Field string

const (
	// FieldHostDisableMonitorStates: a host whose CMDB state contains any of
	// these is not monitored; the access path drops its records.
	FieldHostDisableMonitorStates Field = "host_disable_monitor_states"
	// FieldIsAccessBKData: whether the computing platform is wired in, which
	// decides where CMDB-level aggregated queries go.
	FieldIsAccessBKData Field = "is_access_bk_data"
	// FieldBKDataCMDBLevelTables: the result tables whose CMDB-level queries
	// the computing platform serves.
	FieldBKDataCMDBLevelTables Field = "bkdata_cmdb_level_tables"
	// FieldFileSystemTypeIgnore: file system types the disk metrics exclude.
	FieldFileSystemTypeIgnore Field = "file_system_type_ignore"
)

// Fields is every field in the order they are read.
var Fields = []Field{
	FieldHostDisableMonitorStates, FieldIsAccessBKData, FieldBKDataCMDBLevelTables, FieldFileSystemTypeIgnore,
}

// DBKeyPrefix is where the base platform declares these fields. The DB key
// is what the protocol's config key ends with and what a change event
// names.
const DBKeyPrefix = "base_config.domains.strategy."

// DBKey is the field's full DB key.
func (field Field) DBKey() string {
	return DBKeyPrefix + string(field)
}

// ValidField reports whether field is one of the closed set.
func ValidField(field Field) bool {
	for _, known := range Fields {
		if field == known {
			return true
		}
	}
	return false
}

// Settings is the effective value of every field, as evaluation reads it.
type Settings struct {
	HostDisableMonitorStates []string
	IsAccessBKData           bool
	BKDataCMDBLevelTables    []string
	FileSystemTypeIgnore     []string
}

// CodeDefaults is the bottom of the protocol's fallback chain: the value a
// field has when nothing above declares one. They are the platform's code
// defaults, copied here because the protocol asks every consumer to hold
// them ("等于代码默认值就继续向下回退" needs the default to compare with).
// Each is annotated with where the platform declares it.
func CodeDefaults() Settings {
	return Settings{
		// config/default.py HOST_DISABLE_MONITOR_STATES
		HostDisableMonitorStates: []string{"备用机", "测试中", "故障中"},
		// config/default.py IS_ACCESS_BK_DATA (from BKAPP_IS_ACCESS_BK_DATA, unset)
		IsAccessBKData: false,
		// config/default.py BKDATA_CMDB_LEVEL_TABLES
		BKDataCMDBLevelTables: []string{},
		// config/default.py FILE_SYSTEM_TYPE_IGNORE
		FileSystemTypeIgnore: []string{"iso9660", "tmpfs", "udf"},
	}
}

// Layer is one layer of the fallback chain: a value per field, or none.
// The deployment layer (alarmd's own configuration) and the platform layer
// (what Redis distributes) are both Layers, resolved by the same rule.
type Layer struct {
	HostDisableMonitorStates *[]string
	IsAccessBKData           *bool
	BKDataCMDBLevelTables    *[]string
	FileSystemTypeIgnore     *[]string
}

// Resolve applies the protocol's fallback rule to the layers, highest
// precedence first. A layer's value is taken while the resolved value is
// still the code default; a value equal to the default therefore leaves
// the next layer in play, exactly as the protocol prescribes for its own
// consumers, so that a deployment's own layer is reached the same way the
// platform reaches its YAML. Absent everywhere, the code default stands.
func Resolve(defaults Settings, layers ...Layer) Settings {
	resolved := Settings{
		HostDisableMonitorStates: cloneStrings(defaults.HostDisableMonitorStates),
		IsAccessBKData:           defaults.IsAccessBKData,
		BKDataCMDBLevelTables:    cloneStrings(defaults.BKDataCMDBLevelTables),
		FileSystemTypeIgnore:     cloneStrings(defaults.FileSystemTypeIgnore),
	}
	for _, layer := range layers {
		if layer.HostDisableMonitorStates != nil && equalStrings(resolved.HostDisableMonitorStates, defaults.HostDisableMonitorStates) {
			resolved.HostDisableMonitorStates = cloneStrings(*layer.HostDisableMonitorStates)
		}
		if layer.IsAccessBKData != nil && resolved.IsAccessBKData == defaults.IsAccessBKData {
			resolved.IsAccessBKData = *layer.IsAccessBKData
		}
		if layer.BKDataCMDBLevelTables != nil && equalStrings(resolved.BKDataCMDBLevelTables, defaults.BKDataCMDBLevelTables) {
			resolved.BKDataCMDBLevelTables = cloneStrings(*layer.BKDataCMDBLevelTables)
		}
		if layer.FileSystemTypeIgnore != nil && equalStrings(resolved.FileSystemTypeIgnore, defaults.FileSystemTypeIgnore) {
			resolved.FileSystemTypeIgnore = cloneStrings(*layer.FileSystemTypeIgnore)
		}
	}
	return resolved
}

// Equal reports whether two settings agree on every field.
func (settings Settings) Equal(other Settings) bool {
	return equalStrings(settings.HostDisableMonitorStates, other.HostDisableMonitorStates) &&
		settings.IsAccessBKData == other.IsAccessBKData &&
		equalStrings(settings.BKDataCMDBLevelTables, other.BKDataCMDBLevelTables) &&
		equalStrings(settings.FileSystemTypeIgnore, other.FileSystemTypeIgnore)
}

// ChangedFields names the fields on which two settings differ.
func (settings Settings) ChangedFields(other Settings) []Field {
	var changed []Field
	if !equalStrings(settings.HostDisableMonitorStates, other.HostDisableMonitorStates) {
		changed = append(changed, FieldHostDisableMonitorStates)
	}
	if settings.IsAccessBKData != other.IsAccessBKData {
		changed = append(changed, FieldIsAccessBKData)
	}
	if !equalStrings(settings.BKDataCMDBLevelTables, other.BKDataCMDBLevelTables) {
		changed = append(changed, FieldBKDataCMDBLevelTables)
	}
	if !equalStrings(settings.FileSystemTypeIgnore, other.FileSystemTypeIgnore) {
		changed = append(changed, FieldFileSystemTypeIgnore)
	}
	return changed
}

func cloneStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

// equalStrings compares as lists: order matters, and nil equals empty,
// because the protocol's JSON has no way to say nil.
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
