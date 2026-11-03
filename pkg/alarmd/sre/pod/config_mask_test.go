// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package pod

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// A write's diff shows a ConfigMap the way configmap.get does, and an
// annotation holding something shaped like a key is masked under any name:
// neither path shows what the read masks.
func TestAWriteDiffShowsAConfigMapAsTheReadDoes(t *testing.T) {
	client, _ := fixture()
	provider := mustProvider(t, Options{Client: client, DefaultNamespace: "own", FingerprintKey: testFingerprintKey})
	rootKey := "Q2xhc3NpY0tleU1hdGVyaWFsMTIzNDU2Nzg5MEFCQ0RFRg"
	pem := "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUZ3oX\n-----END CERTIFICATE-----"
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "c", "namespace": "own", "annotations": map[string]any{"example.com/note": pem, "example.com/owner": "team-a"}},
		"data":     map[string]any{"crypt_root_key": rootKey, "host": "databus.data.svc", "app.yaml": "crypt:\n  root_key: " + rootKey + "\nport: 8080\n"}}}
	shown := provider.shown(object, provider.newFingerprints())
	encoded, _ := json.Marshal(shown)
	if strings.Contains(string(encoded), rootKey) || strings.Contains(string(encoded), "MIIBszCC") {
		t.Fatalf("diff shows key material: %s", encoded)
	}
	for _, kept := range []string{`"host":"databus.data.svc"`, "port: 8080", `"example.com/owner":"team-a"`} {
		if !strings.Contains(string(encoded), kept) {
			t.Fatalf("diff lost %s: %s", kept, encoded)
		}
	}
}

// A path of short words between slashes is configuration; a run of base64
// is key material whether it carries a digit or is one long stretch, and
// whether or not slashes cut it.
func TestAPathIsNotKeyMaterialButBase64Is(t *testing.T) {
	for _, path := range []string{"/etc/coredns/NodeHosts", "/var/lib/Kubelet/pods/Volumes", "conf/AlarmDefinitions/Default"} {
		if keyMaterial(path) {
			t.Errorf("path %q read as key material", path)
		}
	}
	for _, key := range []string{"QWxhZGRpbjpvcGVuIHNlc2FtZQ", "ab3D/ef5G/hi7J/kl9M/no1P/qr", "abcDEFghiJKLmnoPQRstuVWX=="} {
		if !keyMaterial(key) {
			t.Errorf("base64 %q not read as key material", key)
		}
	}
}

// Every credential shape is masked where it stands, under a plain name and
// on a plain line, while everything around it - and hosts, addresses and
// paths on their own - is shown as it is.
func TestEachCredentialShapeIsMaskedAndEverythingElseShown(t *testing.T) {
	client, _ := fixture()
	provider := mustProvider(t, Options{Client: client, DefaultNamespace: "own", FingerprintKey: testFingerprintKey})
	shapes := map[string]struct{ text, secret string }{
		"url userinfo":    {"upstream https://ops:Hunter9pw@mirror.example.test/repo", "Hunter9pw"},
		"bearer":          {"curl -H 'Authorization: Bearer abcDEF123ghiJKL456' https://api.example.test", "abcDEF123ghiJKL456"},
		"basic":           {"header Basic dXNlcjpwYXNzd29yZA==", "dXNlcjpwYXNzd29yZA=="},
		"authorization":   {"Authorization=opaque-value-77", "opaque-value-77"},
		"jwt":             {"session eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlLXBhcnQ", "eyJzdWIiOiIxMjM0In0"},
		"hex run":         {"checksum 9f3a9f3a9f3a9f3a9f3a9f3a9f3a9f3a9f3a", "9f3a9f3a9f3a9f3a9f3a9f3a9f3a9f3a"},
		"base64 key":      {"Xk7pQ2mZ9wLrT4vB8nC1dF6gH3jK5sA0", "Xk7pQ2mZ9wLrT4vB8nC1dF6gH3jK5sA0"},
		"aws access key":  {"export AWS_ID=AKIA0123456789ABCDEF", "AKIA0123456789ABCDEF"},
		"github token":    {"clone with ghp_0123456789abcdefghijABCDEFGHIJ", "ghp_0123456789abcdefghijABCDEFGHIJ"},
		"credential word": {"password hunter2", "hunter2"},
		"credential key":  {"db.password: hunter2", "hunter2"},
		"in a comment":    {"# api_key: hunter2", "hunter2"},
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			masker := &configMasker{fingerprints: provider.newFingerprints()}
			shown := masker.value("notes", shape.text+"\nhost: mirror.example.test")
			if strings.Contains(shown, shape.secret) || masker.masked == 0 {
				t.Fatalf("%q shown as %q", shape.text, shown)
			}
			if !strings.Contains(shown, "host: mirror.example.test") {
				t.Fatalf("the plain line around it was lost: %q", shown)
			}
		})
	}
	for _, plain := range []string{"mirror.example.test", "192.0.2.14 node-192-0-2-14", "fe80::1 node-a", "forward . /etc/resolv.conf",
		"hosts /etc/coredns/NodeHosts {", "server.port=8080", "timeout: 30s"} {
		masker := &configMasker{fingerprints: provider.newFingerprints()}
		if shown := masker.value("notes", plain+"\nloop"); shown != plain+"\nloop" || masker.masked != 0 {
			t.Errorf("plain %q shown as %q", plain, shown)
		}
	}
}
