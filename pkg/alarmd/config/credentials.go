// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

// A credential may be given by reference: beside each credential field is
// the same name with a _file suffix, whose value is a path - in a Pod, a key
// of a Secret mounted read-only. The configuration itself then carries no
// credential and can live in a ConfigMap.
//
// A reference is read once, at startup; rotating a credential is a rollout.
// Each credential has exactly one source: the inline value, the file, or -
// for the CLI administrator key and the linkd Console credentials - the
// environment variable they could already come from. Two sources for one
// credential is a deployment that can rotate one and keep using the other,
// so it is refused by the field's configuration path.

// CredentialFileLimit is the most a credential file may hold. A credential
// is a line; a file larger than this is a mounted key of the wrong kind.
const CredentialFileLimit = 4 << 10

// The bounded reasons a reference fails for. A failure names the field's
// configuration path and one of these, never the file's content.
const (
	CredentialNotFound   = "not_found"
	CredentialPermission = "permission"
	CredentialEmpty      = "empty"
	CredentialTooLarge   = "too_large"
	// CredentialMultiline is a file that still holds a line break after its
	// one trailing newline is removed: most often a whole YAML document or a
	// certificate mounted where a single value belongs.
	CredentialMultiline = "multiline"
	// CredentialUnreadable is any other failure to read the path, a
	// directory above all.
	CredentialUnreadable = "unreadable"
	// CredentialInvalidPath is a reference that is not a well-formed
	// absolute path (redact.AcceptedCredentialPath). It is checked before
	// anything is opened, in every mode, and the value is never echoed:
	// the commonest such value is the credential itself, written where its
	// path belongs.
	CredentialInvalidPath = "invalid_path"
)

// CredentialMode says how loading treats credential references.
type CredentialMode int

const (
	// CredentialsForRoles reads the references the selected roles use and
	// no others: a Pod without a Secret key mounted for a connection it
	// never opens still starts. This is how the process starts.
	CredentialsForRoles CredentialMode = iota
	// CredentialsUnread opens no file. It checks the shape of every
	// reference and that each credential has one source, which is what a
	// release check can do on the final YAML where no Secret is mounted.
	CredentialsUnread
	// CredentialsAll reads every reference, for every role, and reports the
	// first that fails. It is for checking a configuration where the files
	// are in place.
	CredentialsAll
)

// unreadCredential stands in, under CredentialsUnread, for a credential
// whose reference was not read, so that the checks which only ask whether
// a credential is given (a CLI key turns the CLI on, a Console username
// needs its password) answer as they would with it read. It satisfies the
// administrator key's own shape so the CLI's checks pass over it. A
// configuration carrying it is checked and discarded, never run.
const unreadCredential = "credential-reference-not-read-by-check-config"

// CredentialReferenceError is a reference that could not be read.
type CredentialReferenceError struct {
	// Field is the reference's configuration path, e.g. redis.password_file.
	Field string
	// Path is the file it names; empty for CredentialInvalidPath, whose
	// value may be the credential itself. Error prints it only when it
	// reads as plain (redact.ShownCredentialPath).
	Path string
	// Reason is one of the bounded reasons above.
	Reason string
}

func (e *CredentialReferenceError) Error() string {
	if e.Reason == CredentialInvalidPath || e.Path == "" {
		return fmt.Sprintf("%s: credential file %s", e.Field, e.Reason)
	}
	if !redact.ShownCredentialPath(e.Path) {
		return fmt.Sprintf("%s (path withheld): credential file %s", e.Field, e.Reason)
	}
	return fmt.Sprintf("%s %q: credential file %s", e.Field, e.Path, e.Reason)
}

// readCredential reads a reference; a variable so a test can count the
// reads.
var readCredential = readCredentialFile

// readCredentialFile reads one credential. Opening follows symbolic links:
// a mounted Secret's keys are links into its ..data directory. The only
// normalization is the removal of one trailing newline ("\n" or "\r\n"),
// which editors and `kubectl create secret --from-file` leave; spaces are
// part of the value, so a password ending in one keeps it.
func readCredentialFile(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return "", credentialOpenReason(err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, CredentialFileLimit+1))
	if err != nil {
		return "", credentialOpenReason(err)
	}
	if len(raw) > CredentialFileLimit {
		return "", CredentialTooLarge
	}
	value := string(raw)
	if strings.HasSuffix(value, "\r\n") {
		value = value[:len(value)-2]
	} else if strings.HasSuffix(value, "\n") {
		value = value[:len(value)-1]
	}
	if value == "" {
		return "", CredentialEmpty
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", CredentialMultiline
	}
	return value, ""
}

func credentialOpenReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return CredentialNotFound
	case errors.Is(err, fs.ErrPermission):
		return CredentialPermission
	default:
		return CredentialUnreadable
	}
}

// credentialReference is one credential and its possible sources.
type credentialReference struct {
	// field is the credential's configuration path, e.g. redis.password;
	// its reference is field + "_file".
	field string
	value *string
	file  *string
	// environment names the variable that may also carry it, if any.
	environment string
}

func redisCredentialReferences(field string, connection *RedisConnectionConfig) []credentialReference {
	if connection == nil {
		return nil
	}
	return []credentialReference{
		{field: field + ".username", value: &connection.Username, file: &connection.UsernameFile},
		{field: field + ".password", value: &connection.Password, file: &connection.PasswordFile},
		{field: field + ".sentinel_username", value: &connection.SentinelUsername, file: &connection.SentinelUsernameFile},
		{field: field + ".sentinel_password", value: &connection.SentinelPassword, file: &connection.SentinelPasswordFile},
	}
}

// credentialGroup is the credentials one part of the process uses, and
// whether the selected roles use that part.
type credentialGroup struct {
	references []credentialReference
	used       bool
}

// credentialGroups lists every credential the configuration can carry,
// grouped by who opens it.
//
// Every process, whatever its roles, opens the top-level redis and the four
// platform caches: Control and Worker for their business runtime, and every
// process - Channel alone included - for the evidence reads its role
// runtime binds to each of them (deploymentReads). Only Control and Worker
// open the compatibility output's service Redis and Pod cache, and only
// they talk to the linkd Console; only Channel serves the CLI.
//
// The alarmd chart mirrors this grouping when it mounts each role only the
// credential keys it reads (credentialConnectionsByRole in the alarmd
// subchart's templates/_credentials.tpl); a change here changes it there.
func (c *Config) credentialGroups() []credentialGroup {
	selected := c.EffectiveRoles()
	business := selected.Has(roles.Control) || selected.Has(roles.Worker)
	var everyRole []credentialReference
	everyRole = append(everyRole, redisCredentialReferences("redis", &c.Redis.RedisConnectionConfig)...)
	everyRole = append(everyRole, redisCredentialReferences("platform_cache.strategy", c.PlatformCache.Strategy)...)
	everyRole = append(everyRole, redisCredentialReferences("platform_cache.cmdb", c.PlatformCache.CMDB)...)
	everyRole = append(everyRole, redisCredentialReferences("platform_cache.dynamic_config", c.PlatformCache.DynamicConfig)...)
	everyRole = append(everyRole, redisCredentialReferences("platform_cache.target_group", c.PlatformCache.TargetGroup)...)

	output := redisCredentialReferences("kafka.legacy_adapter.service_redis", &c.Kafka.LegacyAdapter.ServiceRedis)
	if cache := c.Kafka.LegacyAdapter.PodCache; cache != nil {
		output = append(output, redisCredentialReferences("kafka.legacy_adapter.pod_cache.connection", &cache.Connection)...)
	}
	linkd := []credentialReference{
		// By file only: the secret has no inline field.
		{field: "phase_two.linkd.control_plane.jwt_secret", value: &c.PhaseTwo.Linkd.ControlPlane.JWTSecret, file: &c.PhaseTwo.Linkd.ControlPlane.JWTSecretFile},
	}
	// The span export's token: the Worker sends the spans of the Slots it runs.
	tracing := []credentialReference{
		{field: "phase_two.tracing.token", value: &c.PhaseTwo.Tracing.Token, file: &c.PhaseTwo.Tracing.TokenFile},
	}
	cli := []credentialReference{
		{field: "cli.admin_key", value: &c.CLI.AdminKey, file: &c.CLI.AdminKeyFile, environment: CLIAdminKeyEnvironment},
	}
	return []credentialGroup{
		{references: everyRole, used: true},
		{references: output, used: business},
		{references: linkd, used: business},
		{references: cli, used: selected.Has(roles.Channel)},
		{references: tracing, used: selected.Has(roles.Worker)},
	}
}

// resolveCredentials gives every credential its one value. It runs right
// after decoding and before anything copies a connection - the platform
// caches inheriting the top-level redis above all - so a copy carries the
// resolved value, and a failed reference is reported once, under the path
// the operator wrote. Each reference is cleared once resolved.
func (c *Config) resolveCredentials(mode CredentialMode) error {
	for _, group := range c.credentialGroups() {
		// A credential and its reference both written, or a reference that
		// is not a well-formed absolute path, is a malformed file whoever
		// reads it: checked for every reference, in every mode, before
		// anything is opened.
		for _, reference := range group.references {
			if *reference.value != "" && *reference.file != "" {
				return fmt.Errorf("%s and %s_file are both set", reference.field, reference.field)
			}
			if *reference.file != "" && !redact.AcceptedCredentialPath(*reference.file) {
				return &CredentialReferenceError{Field: reference.field + "_file", Reason: CredentialInvalidPath}
			}
		}
		consulted := group.used || mode == CredentialsAll
		for _, reference := range group.references {
			if *reference.file == "" {
				continue
			}
			if !consulted {
				// Not opened by this process: neither read nor kept.
				*reference.file = ""
				continue
			}
			if reference.environment != "" {
				if env, ok := os.LookupEnv(reference.environment); ok && env != "" {
					return fmt.Errorf("%s_file and %s are both set", reference.field, reference.environment)
				}
			}
			if mode == CredentialsUnread {
				*reference.value, *reference.file = unreadCredential, ""
				continue
			}
			value, reason := readCredential(*reference.file)
			if reason != "" {
				return &CredentialReferenceError{Field: reference.field + "_file", Path: *reference.file, Reason: reason}
			}
			*reference.value, *reference.file = value, ""
		}
	}
	selected := c.EffectiveRoles()
	if mode == CredentialsAll || selected.Has(roles.Control) || selected.Has(roles.Worker) {
	}
	if mode == CredentialsAll || selected.Has(roles.Channel) {
		if err := c.CLI.resolveAdminKeyFromEnvironment(); err != nil {
			return err
		}
	}
	return nil
}
