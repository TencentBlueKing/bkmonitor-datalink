// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package pod

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// alarmd's configuration names its credentials by file. The config view
// shows each reference's path - where the Secret key is mounted, which is
// how a wrong mount is diagnosed - while a credential written inline stays
// masked as before.
func TestTheConfigViewShowsCredentialReferencePathsAndMasksCredentials(t *testing.T) {
	client, _ := fixture()
	provider := mustProvider(t, Options{Client: client, DefaultNamespace: "own", FingerprintKey: testFingerprintKey})
	document := "redis:\n  address: 192.0.2.1:6379\n  password_file: /etc/alarmd/secrets/redis-password\n" +
		"  sentinel_password_file: /etc/alarmd/secrets/redis-sentinel-password\n" +
		"platform_cache:\n  cmdb:\n    password: test-password-1\n" +
		"cli:\n  admin_key_file: /etc/alarmd/secrets/cli-admin-key\n" +
		"phase_two:\n  linkd:\n    username_file: /etc/alarmd/secrets/linkd-username\n    password_file: test-password-2\n" +
		"kafka:\n  legacy_adapter:\n    service_redis:\n      password_file: test-password-placeholder-00000000000000\n" +
		"      sentinel_password_file: secrets/sentinel-password\n      username_file: /Test0password0placeholder\n"
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "alarmd", "namespace": "own"},
		"data":     map[string]any{"alarmd.yaml": document}}}
	encoded, _ := json.Marshal(provider.shown(object, provider.newFingerprints()))
	for _, path := range []string{"/etc/alarmd/secrets/redis-password", "/etc/alarmd/secrets/redis-sentinel-password",
		"/etc/alarmd/secrets/cli-admin-key", "/etc/alarmd/secrets/linkd-username"} {
		if !strings.Contains(string(encoded), path) {
			t.Errorf("the view hides the reference %s: %s", path, encoded)
		}
	}
	// An inline credential stays masked, and so does a value under a _file
	// name that is not a path.
	// A credential written where its path belongs, or a relative path,
	// fails the rule alarmd loads references by, and is masked.
	for _, hidden := range []string{"test-password-1", "test-password-2", "test-password-placeholder-00000000000000", "secrets/sentinel-password", "Test0password0placeholder"} {
		if strings.Contains(string(encoded), hidden) {
			t.Errorf("the view shows %s: %s", hidden, encoded)
		}
	}
}

// Read from the Pod, a reference's path is shown and the credential read
// from it is not: the process hands what it resolved to the redactor as a
// literal secret, as it does an inline one.
func TestOutputShowsAReferencePathButNeverTheCredential(t *testing.T) {
	out, _ := newRedactor([]string{"test-password-1 "}).bounded(
		"password_file: /etc/alarmd/secrets/redis-password\n$ cat /etc/alarmd/secrets/redis-password\ntest-password-1 \n", 4096)
	if !strings.Contains(out, "password_file: /etc/alarmd/secrets/redis-password") || strings.Contains(out, "test-password-1") {
		t.Fatalf("output %q", out)
	}
}
