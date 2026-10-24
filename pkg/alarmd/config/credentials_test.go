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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

const testAdminKey = "test-admin-key-placeholder-0000000000"

// credentialConnections are the seven Redis connections a file can state,
// by configuration path.
var credentialConnections = []string{
	"redis",
	"platform_cache.strategy",
	"platform_cache.cmdb",
	"platform_cache.dynamic_config",
	"platform_cache.target_group",
	"kafka.legacy_adapter.service_redis",
	"kafka.legacy_adapter.pod_cache.connection",
}

// credentialConfig renders a configuration stating every connection, each
// with the extra lines given for its path; "cli" and "phase_two.linkd"
// take extra lines too. Indentation is the helper's.
func credentialConfig(extra map[string][]string) string {
	lines := func(path, indent string) string {
		var b strings.Builder
		for _, line := range extra[path] {
			b.WriteString(indent + line + "\n")
		}
		return b.String()
	}
	connection := func(path, indent, address string) string {
		return indent + "mode: standalone\n" + indent + "address: " + address + "\n" + lines(path, indent)
	}
	platform := ""
	for _, name := range []string{"strategy", "cmdb", "dynamic_config", "target_group"} {
		if _, stated := extra["platform_cache."+name]; stated {
			platform += "  " + name + ":\n" + connection("platform_cache."+name, "    ", "192.0.2.2:6379")
		}
	}
	if _, stated := extra["platform_cache.target_group"]; stated {
		platform += "  dynamic_group_key_prefix: 'groups:'\n"
	}
	if platform != "" {
		platform = "platform_cache:\n" + platform
	}
	podCache := ""
	if _, stated := extra["kafka.legacy_adapter.pod_cache.connection"]; stated {
		podCache = "    pod_cache:\n      key_prefix: pods\n      connection:\n" +
			connection("kafka.legacy_adapter.pod_cache.connection", "        ", "192.0.2.3:6379")
	}
	cli := ""
	if _, stated := extra["cli"]; stated {
		cli = "cli:\n  environment_id: env-test\n  environment_name: test\n  public_base_url: https://alarmd.example.test\n" + lines("cli", "  ")
	}
	linkd := ""
	if _, stated := extra["phase_two.linkd"]; stated {
		linkd = "  linkd:\n    console_url: https://console.example.test/local-api\n" + lines("phase_two.linkd", "    ")
	}
	return fmt.Sprintf(`http:
  listen: 127.0.0.1:8080
kafka:
  brokers: [127.0.0.1:9092]
  trigger_event:
    topic: alarmd-trigger-event
  legacy_adapter:
    topic: alarmd_0bkmonitor_backend_event
    snapshot_prefix: alarmd-test
    service_redis:
%s%sredis:
%s  state_prefix: alarmd:phase-two:g2:v1
%s%sphase_two:
  worker:
    id: cred-worker
  control:
    strategy_cache_prefix: alarm-config
    timezone: Asia/Shanghai
  access:
    uq_endpoint: http://unify-query.service
    query_source: alarmd
%s`, connection("kafka.legacy_adapter.service_redis", "      ", "192.0.2.4:6379"), podCache,
		connection("redis", "  ", "192.0.2.1:6379"), platform, cli, linkd)
}

func writeCredential(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearCredentialEnvironment keeps the process's own environment out of a
// test: an empty variable is no source.
func clearCredentialEnvironment(t *testing.T) {
	for _, name := range []string{CLIAdminKeyEnvironment, CLIAdminKeySecretNameEnvironment, CLIAdminKeySecretKeyEnvironment,
		LinkdConsoleUsernameEnvironment, LinkdConsolePasswordEnvironment} {
		t.Setenv(name, "")
	}
}

func connectionAt(t *testing.T, cfg Config, path string) RedisConnectionConfig {
	t.Helper()
	switch path {
	case "redis":
		return cfg.Redis.RedisConnectionConfig
	case "platform_cache.strategy":
		return *cfg.PlatformCache.Strategy
	case "platform_cache.cmdb":
		return *cfg.PlatformCache.CMDB
	case "platform_cache.dynamic_config":
		return *cfg.PlatformCache.DynamicConfig
	case "platform_cache.target_group":
		return *cfg.PlatformCache.TargetGroup
	case "kafka.legacy_adapter.service_redis":
		return cfg.Kafka.LegacyAdapter.ServiceRedis
	case "kafka.legacy_adapter.pod_cache.connection":
		return cfg.Kafka.LegacyAdapter.PodCache.Connection
	}
	t.Fatalf("no connection %s", path)
	return RedisConnectionConfig{}
}

// Each of the seven connections takes all four of its credentials from
// files, and the loaded configuration carries the values with every
// reference cleared.
func TestEveryRedisConnectionTakesItsCredentialsFromFiles(t *testing.T) {
	clearCredentialEnvironment(t)
	extra := map[string][]string{}
	for index, path := range credentialConnections {
		extra[path] = []string{
			"username_file: " + writeCredential(t, "username", fmt.Sprintf("test-user-%d\n", index)),
			"password_file: " + writeCredential(t, "password", fmt.Sprintf("test-password-%d\n", index)),
			"sentinel_username_file: " + writeCredential(t, "sentinel-username", fmt.Sprintf("test-sentinel-user-%d", index)),
			"sentinel_password_file: " + writeCredential(t, "sentinel-password", fmt.Sprintf("test-sentinel-password-%d\r\n", index)),
		}
	}
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(extra)), nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, path := range credentialConnections {
		connection := connectionAt(t, cfg, path)
		want := RedisConnectionConfig{Username: fmt.Sprintf("test-user-%d", index), Password: fmt.Sprintf("test-password-%d", index),
			SentinelUsername: fmt.Sprintf("test-sentinel-user-%d", index), SentinelPassword: fmt.Sprintf("test-sentinel-password-%d", index)}
		if connection.Username != want.Username || connection.Password != want.Password ||
			connection.SentinelUsername != want.SentinelUsername || connection.SentinelPassword != want.SentinelPassword {
			t.Errorf("%s credentials = %q/%q/%q/%q, want %+v", path, connection.Username, connection.Password,
				connection.SentinelUsername, connection.SentinelPassword, want)
		}
		if connection.UsernameFile != "" || connection.PasswordFile != "" || connection.SentinelUsernameFile != "" || connection.SentinelPasswordFile != "" {
			t.Errorf("%s kept a reference after reading it: %+v", path, connection)
		}
	}
}

// A platform cache that inherits the top-level connection inherits the
// resolved password: references are read before the copy. The copy, and
// every clone handed out afterwards, carries the value and no reference.
func TestAnInheritingCacheCarriesTheResolvedPassword(t *testing.T) {
	clearCredentialEnvironment(t)
	password := writeCredential(t, "password", "test-password-1\n")
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + password}})), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlatformCache.Strategy == nil || cfg.PlatformCache.CMDB == nil {
		t.Fatal("the platform caches did not inherit the top-level connection")
	}
	for name, connection := range map[string]RedisConnectionConfig{
		"strategy (copied)": *cfg.PlatformCache.Strategy, "cmdb (copied)": *cfg.PlatformCache.CMDB,
		"redis clone": cfg.Redis.RedisConnectionConfig.clone(), "runtime store": cfg.RuntimeStoreRedis(),
		"strategy source": cfg.StrategySourceRedis(), "cmdb cache": cfg.CMDBCacheRedis(),
	} {
		if connection.Password != "test-password-1" || connection.PasswordFile != "" {
			t.Errorf("%s: password %q, reference %q; want the resolved value and no reference", name, connection.Password, connection.PasswordFile)
		}
	}
}

// Each reference is read once: the inheriting caches copy the value, not
// the reference, so they never read the top-level file again.
func TestEachReferenceIsReadOnce(t *testing.T) {
	clearCredentialEnvironment(t)
	reads := map[string]int{}
	t.Cleanup(func() { readCredential = readCredentialFile })
	readCredential = func(path string) (string, string) {
		reads[path]++
		return readCredentialFile(path)
	}
	password := writeCredential(t, "password", "test-password-1\n")
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + password}})), nil)
	if err != nil {
		t.Fatal(err)
	}
	if reads[password] != 1 || len(reads) != 1 {
		t.Fatalf("reads = %v, want the top-level reference read once", reads)
	}
	if cfg.CMDBCacheRedis().Password != "test-password-1" {
		t.Fatal("the inheriting cache lost the password")
	}
}

// A top-level reference that fails is reported once, under the path the
// operator wrote - not again under each cache that would have copied it.
func TestAFailedReferenceIsReportedUnderThePathWritten(t *testing.T) {
	clearCredentialEnvironment(t)
	missing := filepath.Join(t.TempDir(), "absent")
	_, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + missing}})), nil)
	var reference *CredentialReferenceError
	if !errors.As(err, &reference) || reference.Field != "redis.password_file" || reference.Reason != CredentialNotFound {
		t.Fatalf("error = %v, want redis.password_file not_found", err)
	}
	if strings.Contains(err.Error(), "platform_cache") {
		t.Fatalf("the error names a path the operator did not write: %v", err)
	}
}

// Each failure names its bounded reason and the field, and never what the
// file holds.
func TestEachFailedReferenceNamesItsReasonAndNotTheContent(t *testing.T) {
	clearCredentialEnvironment(t)
	dir := t.TempDir()
	cases := []struct {
		name, reason string
		path         func() string
	}{
		{"absent", CredentialNotFound, func() string { return filepath.Join(dir, "absent") }},
		{"empty", CredentialEmpty, func() string { return writeCredential(t, "empty", "") }},
		{"newline only", CredentialEmpty, func() string { return writeCredential(t, "newline", "\n") }},
		{"too large", CredentialTooLarge, func() string {
			return writeCredential(t, "large", "test-password-"+strings.Repeat("x", CredentialFileLimit))
		}},
		{"two lines", CredentialMultiline, func() string { return writeCredential(t, "two", "test-password-1\ntest-password-2\n") }},
		{"two trailing newlines", CredentialMultiline, func() string { return writeCredential(t, "trailing", "test-password-1\n\n") }},
		{"carriage return", CredentialMultiline, func() string { return writeCredential(t, "cr", "test-password-1\r") }},
		{"directory", CredentialUnreadable, func() string { return t.TempDir() }},
	}
	if os.Geteuid() != 0 {
		cases = append(cases, struct {
			name, reason string
			path         func() string
		}{"no permission", CredentialPermission, func() string {
			path := writeCredential(t, "denied", "test-password-1\n")
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			return path
		}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := test.path()
			_, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{
				"kafka.legacy_adapter.service_redis": {"password_file: " + path}})), nil)
			var reference *CredentialReferenceError
			if !errors.As(err, &reference) || reference.Reason != test.reason ||
				reference.Field != "kafka.legacy_adapter.service_redis.password_file" || reference.Path != path {
				t.Fatalf("error = %v, want %s under kafka.legacy_adapter.service_redis.password_file", err, test.reason)
			}
			if strings.Contains(err.Error(), "test-password") {
				t.Fatalf("the error carries the file's content: %v", err)
			}
		})
	}
}

// The only normalization is one trailing newline: a password that ends in
// a space keeps it, and one exactly at the limit is read.
func TestOnlyOneTrailingNewlineIsRemoved(t *testing.T) {
	atLimit := strings.Repeat("p", CredentialFileLimit-1) + "\n"
	for content, want := range map[string]string{
		"test-password-1 \n":   "test-password-1 ",
		"test-password-1 ":     "test-password-1 ",
		" test-password-1\r\n": " test-password-1",
		"\ttest-password-1\t":  "\ttest-password-1\t",
		atLimit:                strings.Repeat("p", CredentialFileLimit-1),
	} {
		value, reason := readCredentialFile(writeCredential(t, "password", content))
		if reason != "" || value != want {
			t.Errorf("%q read as %q (%s), want %q", content, value, reason, want)
		}
	}
}

// A mounted Secret's key is a symbolic link into its ..data directory; the
// reference follows it.
func TestAReferenceFollowsTheSecretVolumesLinks(t *testing.T) {
	clearCredentialEnvironment(t)
	volume := t.TempDir()
	revision := filepath.Join(volume, "..2026_10_21_00_00_00.000000001")
	if err := os.Mkdir(revision, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(revision, "redis-password"), []byte("test-password-1 \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(revision), filepath.Join(volume, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "redis-password"), filepath.Join(volume, "redis-password")); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{
		"redis": {"password_file: " + filepath.Join(volume, "redis-password")}})), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Redis.Password != "test-password-1 " {
		t.Fatalf("password = %q through the volume's links", cfg.Redis.Password)
	}
}

// One credential has one source. The inline value and its reference are
// refused together in every mode, as is a reference beside the environment
// variable that can carry the same credential.
func TestACredentialWithTwoSourcesIsRefused(t *testing.T) {
	file := writeCredential(t, "credential", testAdminKey+"\n")
	cases := []struct {
		name  string
		extra map[string][]string
		env   map[string]string
		want  string
	}{
		{"redis value and file", map[string][]string{"redis": {"password: test-password-1", "password_file: " + file}}, nil,
			"redis.password and redis.password_file are both set"},
		{"cache value and file", map[string][]string{"platform_cache.cmdb": {"sentinel_username: test-user-1", "sentinel_username_file: " + file}}, nil,
			"platform_cache.cmdb.sentinel_username and platform_cache.cmdb.sentinel_username_file are both set"},
		{"admin key value and file", map[string][]string{"cli": {"admin_key: " + testAdminKey, "admin_key_file: " + file}}, nil,
			"cli.admin_key and cli.admin_key_file are both set"},
		{"admin key environment and file", map[string][]string{"cli": {"admin_key_file: " + file}},
			map[string]string{CLIAdminKeyEnvironment: testAdminKey}, "cli.admin_key_file and " + CLIAdminKeyEnvironment + " are both set"},
		{"admin key environment and value", map[string][]string{"cli": {"admin_key: " + testAdminKey}},
			map[string]string{CLIAdminKeyEnvironment: testAdminKey}, "cli admin_key is set both in the file and in " + CLIAdminKeyEnvironment},
		{"console password environment and file", map[string][]string{"phase_two.linkd": {"username: reader", "password_file: " + file}},
			map[string]string{LinkdConsolePasswordEnvironment: "test-password-1"}, "phase_two.linkd.password_file and " + LinkdConsolePasswordEnvironment + " are both set"},
		{"console username value and file", map[string][]string{"phase_two.linkd": {"username: reader", "username_file: " + file, "password: test-password-1"}}, nil,
			"phase_two.linkd.username and phase_two.linkd.username_file are both set"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clearCredentialEnvironment(t)
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			path := writeConfig(t, credentialConfig(test.extra))
			for _, mode := range []CredentialMode{CredentialsForRoles, CredentialsUnread, CredentialsAll} {
				_, err := LoadWithCredentials(path, nil, mode)
				if err == nil || err.Error() != test.want {
					t.Fatalf("mode %d: error = %v, want %q", mode, err, test.want)
				}
			}
		})
	}
}

// The Secret's name and key are where the administrator key is kept, for
// the login page, not a source of it: today's chart sets them beside the
// key's environment variable, and they may stand beside a file as well.
func TestTheAdminKeySecretNameIsNotASourceOfTheKey(t *testing.T) {
	clearCredentialEnvironment(t)
	t.Setenv(CLIAdminKeySecretNameEnvironment, "alarmd-cli-admin")
	t.Setenv(CLIAdminKeySecretKeyEnvironment, "admin-key")

	t.Setenv(CLIAdminKeyEnvironment, testAdminKey)
	chart, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"cli": nil})), nil)
	if err != nil {
		t.Fatalf("the chart's environment key and Secret name refused the load: %v", err)
	}
	if chart.CLI.AdminKey != testAdminKey || chart.CLI.AdminKeySecret.Name != "alarmd-cli-admin" {
		t.Fatalf("key %t, secret %+v", chart.CLI.AdminKey == testAdminKey, chart.CLI.AdminKeySecret)
	}

	t.Setenv(CLIAdminKeyEnvironment, "")
	file := writeCredential(t, "admin-key", testAdminKey+"\n")
	fromFile, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"cli": {"admin_key_file: " + file}})), nil)
	if err != nil {
		t.Fatalf("a key file beside the Secret name refused the load: %v", err)
	}
	if fromFile.CLI.AdminKey != testAdminKey || !fromFile.CLI.Enabled() || fromFile.CLI.AdminKeySecret.Name != "alarmd-cli-admin" || fromFile.CLI.AdminKeyFile != "" {
		t.Fatalf("key %t, enabled %t, secret %+v, reference %q", fromFile.CLI.AdminKey == testAdminKey,
			fromFile.CLI.Enabled(), fromFile.CLI.AdminKeySecret, fromFile.CLI.AdminKeyFile)
	}
}

// The linkd Console credentials come from files like the rest.
func TestTheConsoleCredentialsComeFromFiles(t *testing.T) {
	clearCredentialEnvironment(t)
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"phase_two.linkd": {
		"username_file: " + writeCredential(t, "username", "reader\n"),
		"password_file: " + writeCredential(t, "password", "test-password-1\n")}})), nil)
	if err != nil {
		t.Fatal(err)
	}
	linkd := cfg.PhaseTwo.Linkd
	if linkd.Username != "reader" || linkd.Password != "test-password-1" || linkd.UsernameFile != "" || linkd.PasswordFile != "" {
		t.Fatalf("console credentials = %q/%q, references %q/%q", linkd.Username, linkd.Password, linkd.UsernameFile, linkd.PasswordFile)
	}
}

// A process reads only the references its roles use: a Pod that does not
// mount a Secret key for a connection it never opens still starts, while a
// reference its roles do use is read and fails when absent.
func TestOnlyTheReferencesTheRolesUseAreRead(t *testing.T) {
	clearCredentialEnvironment(t)
	absent := filepath.Join(t.TempDir(), "absent")
	cases := []struct {
		name      string
		roles     roles.Set
		extra     map[string][]string
		refusedAs string
	}{
		{"channel does not open the compatibility output", roles.Set{roles.Channel},
			map[string][]string{"kafka.legacy_adapter.service_redis": {"password_file: " + absent},
				"kafka.legacy_adapter.pod_cache.connection": {"password_file: " + absent}}, ""},
		{"channel does not talk to the console", roles.Set{roles.Channel},
			map[string][]string{"phase_two.linkd": {"username_file: " + absent, "password_file: " + absent}}, ""},
		{"control does not serve the CLI", roles.Set{roles.Control},
			map[string][]string{"cli": {"admin_key_file: " + absent}}, ""},
		{"worker does not serve the CLI", roles.Set{roles.Worker},
			map[string][]string{"cli": {"admin_key_file: " + absent}}, ""},
		{"worker opens the compatibility output", roles.Set{roles.Worker},
			map[string][]string{"kafka.legacy_adapter.pod_cache.connection": {"password_file: " + absent}}, "kafka.legacy_adapter.pod_cache.connection.password_file"},
		{"control talks to the console", roles.Set{roles.Control},
			map[string][]string{"phase_two.linkd": {"username: reader", "password_file: " + absent}}, "phase_two.linkd.password_file"},
		{"channel serves the CLI", roles.Set{roles.Channel},
			map[string][]string{"cli": {"admin_key_file: " + absent}}, "cli.admin_key_file"},
		{"channel opens the runtime store", roles.Set{roles.Channel},
			map[string][]string{"redis": {"password_file: " + absent}}, "redis.password_file"},
		{"channel opens the platform caches", roles.Set{roles.Channel},
			map[string][]string{"platform_cache.dynamic_config": {"password_file: " + absent}}, "platform_cache.dynamic_config.password_file"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadWithRoles(writeConfig(t, credentialConfig(test.extra)), test.roles)
			if test.refusedAs == "" {
				if err != nil {
					t.Fatalf("a reference the roles do not use refused the load: %v", err)
				}
				return
			}
			var reference *CredentialReferenceError
			if !errors.As(err, &reference) || reference.Field != test.refusedAs || reference.Reason != CredentialNotFound {
				t.Fatalf("error = %v, want %s not_found", err, test.refusedAs)
			}
		})
	}
}

// Unread, no file is opened: absent references load, and the checks that
// ask whether a credential is given answer as if it were. Reading every
// reference reports the first that fails, whatever the roles.
func TestTheCheckModesReadNothingOrEverything(t *testing.T) {
	clearCredentialEnvironment(t)
	absent := filepath.Join(t.TempDir(), "absent")
	text := credentialConfig(map[string][]string{
		"redis":                              {"password_file: " + absent},
		"kafka.legacy_adapter.service_redis": {"password_file: " + absent},
		"cli":                                {"admin_key_file: " + absent},
		"phase_two.linkd":                    {"username_file: " + absent, "password_file: " + absent},
	})
	path := writeConfig(t, text)
	cfg, err := LoadWithCredentials(path, nil, CredentialsUnread)
	if err != nil {
		t.Fatalf("unread, an absent reference refused the load: %v", err)
	}
	if !cfg.CLI.Enabled() || cfg.CLI.AdminKeyFile != "" || cfg.Redis.PasswordFile != "" {
		t.Fatalf("unread: CLI enabled %t, references %q %q", cfg.CLI.Enabled(), cfg.CLI.AdminKeyFile, cfg.Redis.PasswordFile)
	}
	if _, err := LoadWithCredentials(path, nil, CredentialsForRoles); err == nil {
		t.Fatal("reading for the roles, an absent reference loaded")
	}

	channelOnly := credentialConfig(map[string][]string{"kafka.legacy_adapter.service_redis": {"password_file: " + absent}})
	if _, err := LoadWithCredentials(writeConfig(t, channelOnly), roles.Set{roles.Channel}, CredentialsForRoles); err != nil {
		t.Fatalf("channel alone read a reference it does not use: %v", err)
	}
	_, err = LoadWithCredentials(writeConfig(t, channelOnly), roles.Set{roles.Channel}, CredentialsAll)
	var reference *CredentialReferenceError
	if !errors.As(err, &reference) || reference.Field != "kafka.legacy_adapter.service_redis.password_file" {
		t.Fatalf("reading everything, error = %v; want the compatibility output's reference", err)
	}
}

// Inline credentials keep working as they always have.
func TestInlineCredentialsStillLoad(t *testing.T) {
	clearCredentialEnvironment(t)
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{
		"redis":           {"password: test-password-1"},
		"cli":             {"admin_key: " + testAdminKey},
		"phase_two.linkd": {"username: reader", "password: test-password-2"},
	})), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Redis.Password != "test-password-1" || cfg.StrategySourceRedis().Password != "test-password-1" ||
		cfg.CLI.AdminKey != testAdminKey || cfg.PhaseTwo.Linkd.Password != "test-password-2" {
		t.Fatal("inline credentials were not loaded as written")
	}
}

// testMistypedToken is a credential written where its path belongs: forty
// characters, no slash.
const testMistypedToken = "test-password-placeholder-00000000000000"

// A reference must be a well-formed absolute path, checked before anything
// is opened and in every mode, so --check-config catches it without the files.
// The refusal names the field and the reason and never the value, which is
// most often the credential itself.
func TestAReferenceThatIsNotAPlainAbsolutePathIsRefusedWithoutEchoingIt(t *testing.T) {
	clearCredentialEnvironment(t)
	if len(testMistypedToken) != 40 {
		t.Fatal("the fixture is not forty characters")
	}
	// The credential itself, a relative path, a control character, leading
	// and trailing whitespace, and a path over the limit.
	for _, value := range []string{testMistypedToken, "secrets/redis-password", "/etc/alarmd/secrets/redis\tpassword",
		" /etc/alarmd/secrets/redis-password", "/etc/alarmd/secrets/redis-password ", "/" + strings.Repeat("p", 4096)} {
		for _, test := range []struct {
			path  string
			roles roles.Set
		}{
			{"redis", nil},
			// Shape is the file's, whoever reads it: refused even where the
			// role does not open the connection.
			{"kafka.legacy_adapter.service_redis", roles.Set{roles.Channel}},
		} {
			text := credentialConfig(map[string][]string{test.path: {fmt.Sprintf("password_file: %q", value)}})
			path := writeConfig(t, text)
			for _, mode := range []CredentialMode{CredentialsForRoles, CredentialsUnread, CredentialsAll} {
				_, err := LoadWithCredentials(path, test.roles, mode)
				var reference *CredentialReferenceError
				if !errors.As(err, &reference) || reference.Reason != CredentialInvalidPath || reference.Field != test.path+".password_file" || reference.Path != "" {
					t.Fatalf("%q at %s, mode %d: error = %v, want %s.password_file invalid_path", value, test.path, mode, err, test.path)
				}
				if want := test.path + ".password_file: credential file invalid_path"; err.Error() != want {
					t.Fatalf("error = %q, want %q", err.Error(), want)
				}
			}
		}
	}
}

// The error never prints the value of a reference refused for its shape,
// even when one is at hand.
func TestAnInvalidPathErrorNamesOnlyTheField(t *testing.T) {
	err := &CredentialReferenceError{Field: "redis.password_file", Path: testMistypedToken, Reason: CredentialInvalidPath}
	if err.Error() != "redis.password_file: credential file invalid_path" {
		t.Fatalf("error = %q", err.Error())
	}
}

// Accepting a path is not judging how it reads: a path with a mixed-case
// word and digits - an operator's choice, or a test's own temporary
// directory - is opened and read. Printed in an error, a path that does not
// read as plain is withheld; a plain one is shown.
func TestAnyWellFormedAbsolutePathIsReadAndOnlyAPlainOneIsPrinted(t *testing.T) {
	clearCredentialEnvironment(t)
	if !redact.AcceptedCredentialPath("/etc/secrets/Redis6Password") || redact.ShownCredentialPath("/etc/secrets/Redis6Password") {
		t.Fatal("/etc/secrets/Redis6Password must be accepted and not shown")
	}
	dir := t.TempDir()
	present := filepath.Join(dir, "Redis6Password")
	if err := os.WriteFile(present, []byte("test-password-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + present}})), nil)
	if err != nil {
		t.Fatalf("a well-formed absolute path was refused: %v", err)
	}
	if cfg.Redis.Password != "test-password-1" {
		t.Fatalf("password = %q", cfg.Redis.Password)
	}

	missing := filepath.Join(dir, "Missing6Password")
	_, err = LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + missing}})), nil)
	var reference *CredentialReferenceError
	if !errors.As(err, &reference) || reference.Reason != CredentialNotFound {
		t.Fatalf("error = %v, want not_found", err)
	}
	if want := "redis.password_file (path withheld): credential file not_found"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}

	plain := "/nonexistent/alarmd/secrets/redis-password"
	_, err = LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + plain}})), nil)
	if err == nil || !strings.Contains(err.Error(), plain) || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("error = %v, want the plain path and not_found", err)
	}
}

// A value shaped like a key behind a slash is a well-formed path, so it is
// accepted and opened; when it is missing, the error withholds it. This
// pins the display check's job: the acceptance check alone would echo it.
func TestAKeyShapedPathIsWithheldFromTheError(t *testing.T) {
	clearCredentialEnvironment(t)
	value := "/dGVzdC1wYXNzd29yZC1wbGFjZWhvbGRlci0wMDAw"
	if len(value) != 41 || !redact.AcceptedCredentialPath(value) {
		t.Fatal("the fixture is not a well-formed path of forty base64 characters")
	}
	_, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + value}})), nil)
	var reference *CredentialReferenceError
	if !errors.As(err, &reference) || reference.Reason != CredentialNotFound {
		t.Fatalf("error = %v, want not_found", err)
	}
	if !strings.Contains(err.Error(), "(path withheld)") || strings.Contains(err.Error(), value[1:]) {
		t.Fatalf("error = %q, want the path withheld", err.Error())
	}
}

// No error carries the operating system's text, which embeds the path:
// only the bounded reason and a vetted or withheld path reach the message,
// for every reason a file fails to open or read.
func TestNoReferenceErrorCarriesTheOperatingSystemsText(t *testing.T) {
	clearCredentialEnvironment(t)
	dir := t.TempDir()
	cases := map[string]func() string{
		CredentialNotFound: func() string { return filepath.Join(dir, "Absent6Password") },
		CredentialUnreadable: func() string {
			path := filepath.Join(dir, "Directory6Password")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			return path
		},
	}
	if os.Geteuid() != 0 {
		cases[CredentialPermission] = func() string {
			path := filepath.Join(dir, "Denied6Password")
			if err := os.WriteFile(path, []byte("test-password-1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			return path
		}
	}
	for reason, prepare := range cases {
		path := prepare()
		if redact.ShownCredentialPath(path) {
			t.Fatalf("%s: the fixture path %q reads as plain", reason, path)
		}
		_, err := LoadWithRoles(writeConfig(t, credentialConfig(map[string][]string{"redis": {"password_file: " + path}})), nil)
		want := "redis.password_file (path withheld): credential file " + reason
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
		if strings.Contains(err.Error(), filepath.Base(path)) || strings.Contains(err.Error(), dir) {
			t.Fatalf("%s: the error carries the path: %q", reason, err.Error())
		}
	}
}
