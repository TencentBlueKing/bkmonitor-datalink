// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pod

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// editSecrets are the stored values no answer may carry.
var editSecrets = []string{"Zm9vYmFyQmF6UXV4MTIzNDU2Nzg5MEFCQ0RFRg", "Sh0rtPw9xyz", "0123456789abcdef0123456789abcdef"}

const editConfig = `server:
  url: http://old.example.test:8080/api
  timeout: 30s
crypt:
  root_key: Zm9vYmFyQmF6UXV4MTIzNDU2Nzg5MEFCQ0RFRg==
  iv: 0123456789abcdef0123456789abcdef
database:
  dsn_password: Sh0rtPw9xyz
# the address before that was http://older.example.test:8080/api
`

func editConfigMap(value string) *unstructured.Unstructured {
	object := configMap("ns1", "app", map[string]any{"config.yaml": value})
	object.SetUID(types.UID("uid-app"))
	object.SetResourceVersion("41")
	return object
}

func editProvider(t *testing.T, value string) (*Provider, *fakeDryRunner, *patchingWriter) {
	t.Helper()
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/app": editConfigMap(value)}}
	writer := &patchingWriter{}
	provider := writeProvider(t, runner, &writer.fakeWriter)
	writer.runner = runner
	provider.options.Writer = writer
	return provider, runner, writer
}

func editRequest(provider *Provider, value, oldText, newText string) ConfigMapEditRequest {
	return ConfigMapEditRequest{Namespace: "ns1", Name: "app", Key: "config.yaml", OldText: oldText, NewText: newText,
		ExpectedFingerprint: provider.newFingerprints().sum([]byte(value))}
}

func storedValue(runner *fakeDryRunner) string {
	return runner.live["ns1/app"].Object["data"].(map[string]any)["config.yaml"].(string)
}

func editCode(err error) string {
	var refusal *Error
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

func carriesNoSecret(t *testing.T, receipt ConfigMapEditReceipt, err error) {
	t.Helper()
	encoded, _ := json.Marshal(receipt)
	text := string(encoded)
	if err != nil {
		text += err.Error()
	}
	for _, secret := range editSecrets {
		if strings.Contains(text, secret) {
			t.Fatalf("a stored secret came back: %s", text)
		}
	}
}

// A URL inside a whole YAML file is re-pointed where configmap.get shows it:
// the stored value changes in exactly that place (not in the comment, which
// names another address), under a patch that tests the uid, the resourceVersion and
// the stored value; the receipt says where and gives fingerprints, never a
// stored value.
func TestAShownURLInsideAConfigFileIsRepointed(t *testing.T) {
	provider, runner, writer := editProvider(t, editConfig)
	request := editRequest(provider, editConfig, "http://old.example.test:8080/api", "http://new.example.test:8080/api")
	receipt, err := provider.EditConfigMapText(context.Background(), request)
	carriesNoSecret(t, receipt, err)
	if err != nil || receipt.Outcome != WriteUpdated {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	want := strings.Replace(editConfig, "url: http://old.example.test:8080/api", "url: http://new.example.test:8080/api", 1)
	if got := storedValue(runner); got != want {
		t.Fatalf("stored after:\n%s\nwant:\n%s", got, want)
	}
	if receipt.Occurrences != 1 || len(receipt.ChangedLines) != 1 || receipt.ChangedLines[0] != 2 ||
		receipt.BeforeFingerprint != request.ExpectedFingerprint || receipt.AfterFingerprint != provider.newFingerprints().sum([]byte(want)) ||
		len(receipt.Shown) != 1 || !strings.Contains(receipt.Shown[0].Text, "http://new.example.test:8080/api") {
		t.Fatalf("receipt %+v", receipt)
	}
	if len(writer.patches) != 1 || !strings.Contains(writer.patches[0], `{"op":"test","path":"/metadata/resourceVersion","value":"41"}`) ||
		!strings.Contains(writer.patches[0], `{"op":"test","path":"/data/config.yaml"`) {
		t.Fatalf("patch %v", writer.patches)
	}
}

// A dry run makes every check and writes nothing.
func TestADryRunEditWritesNothing(t *testing.T) {
	provider, runner, writer := editProvider(t, editConfig)
	request := editRequest(provider, editConfig, "http://old.example.test:8080/api", "http://new.example.test:8080/api")
	request.DryRun = true
	receipt, err := provider.EditConfigMapText(context.Background(), request)
	if err != nil || receipt.Outcome != EditChecked || receipt.Write != nil || len(writer.patches) != 0 || storedValue(runner) != editConfig ||
		len(receipt.ChangedLines) != 1 || receipt.AfterFingerprint == "" {
		t.Fatalf("receipt %+v err %v patches %v", receipt, err, writer.patches)
	}
}

// A key that changed since it was read is not edited.
func TestAnEditOfAKeyThatChangedIsRefused(t *testing.T) {
	provider, _, writer := editProvider(t, editConfig)
	request := editRequest(provider, editConfig+"extra: 1\n", "http://old.example.test:8080/api", "http://new.example.test:8080/api")
	receipt, err := provider.EditConfigMapText(context.Background(), request)
	if editCode(err) != EditFingerprintChanged || len(writer.patches) != 0 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
}

// old_text must be shown exactly once: not missing, not twice.
func TestOldTextMustBeShownExactlyOnce(t *testing.T) {
	twice := editConfig + "backup:\n  url: http://old.example.test:8080/api\n"
	for _, value := range []string{editConfig, twice} {
		provider, _, writer := editProvider(t, value)
		oldText := "http://old.example.test:8080/api"
		if value == editConfig {
			oldText = "http://nowhere.example.test/"
		}
		_, err := provider.EditConfigMapText(context.Background(), editRequest(provider, value, oldText, "http://new.example.test/"))
		if editCode(err) != EditOldTextNotShown || len(writer.patches) != 0 {
			t.Fatalf("old_text %q: err %v", oldText, err)
		}
	}
}

// Text inside a masked value is not editable, even when it occurs once in
// the stored value: the answer must not say whether a guess was there. A
// guess that is there and one that is not are refused alike.
func TestTextInsideAMaskedValueIsRefusedLikeTextThatIsNowhere(t *testing.T) {
	for _, guess := range []string{"Sh0rtPw9", "Sh0rtPw8"} {
		provider, runner, writer := editProvider(t, editConfig)
		receipt, err := provider.EditConfigMapText(context.Background(), editRequest(provider, editConfig, guess, "replaced"))
		carriesNoSecret(t, receipt, err)
		if editCode(err) != EditOldTextNotShown || len(writer.patches) != 0 || storedValue(runner) != editConfig {
			t.Fatalf("guess %q: err %v", guess, err)
		}
	}
}

// Text spanning a masked value and plain text is not editable either.
func TestTextSpanningAMaskedValueIsRefused(t *testing.T) {
	provider, _, writer := editProvider(t, editConfig)
	_, err := provider.EditConfigMapText(context.Background(), editRequest(provider, editConfig, "dsn_password: Sh0rt", "dsn_host: x"))
	if editCode(err) != EditOldTextNotShown || len(writer.patches) != 0 {
		t.Fatalf("err %v", err)
	}
}

// Whether a masked value also holds the shown text changes nothing: the edit
// lands where the text is shown, the masked value is left as it was, and the
// answer is the same as for a file whose masked value does not hold it.
func TestAMaskedCopyOfShownTextNeitherBlocksNorReceivesTheEdit(t *testing.T) {
	withCopy := strings.Replace(editConfig, "dsn_password: Sh0rtPw9xyz", "dsn_password: Sh0rtPw9xyz-old.example.test", 1)
	for _, value := range []string{editConfig, withCopy} {
		provider, runner, _ := editProvider(t, value)
		receipt, err := provider.EditConfigMapText(context.Background(), editRequest(provider, value, "old.example.test", "new.example.test"))
		if err != nil || receipt.Outcome != WriteUpdated || receipt.ChangedLines[0] != 2 {
			t.Fatalf("receipt %+v err %v", receipt, err)
		}
		got := storedValue(runner)
		if !strings.Contains(got, "url: http://new.example.test:8080/api") ||
			(value == withCopy && !strings.Contains(got, "dsn_password: Sh0rtPw9xyz-old.example.test")) {
			t.Fatalf("stored after:\n%s", got)
		}
	}
}

// Text only a placeholder shows is not stored anywhere to edit.
func TestAPlaceholderIsNotEditableText(t *testing.T) {
	value := "server:\n  url: http://old.example.test/\n  build_id: Zm9vYmFyQmF6UXV4MTIzNDU2Nzg5MEFCQ0RFRg==\n"
	provider, _, writer := editProvider(t, value)
	_, err := provider.EditConfigMapText(context.Background(), editRequest(provider, value, "<redacted bytes=", "x"))
	if editCode(err) != EditNotShownExactly || len(writer.patches) != 0 {
		t.Fatalf("err %v", err)
	}
}

// new_text that would be masked is refused: key material on its own, and
// plain words that would put the value under a credential-like name.
func TestNewTextThatWouldBeMaskedIsRefused(t *testing.T) {
	for _, newText := range []string{
		"QWxhZGRpbjpvcGVuIHNlc2FtZQpLZXlNYXRlcmlhbA==",
		"-----BEGIN PRIVATE KEY-----",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature",
	} {
		provider, _, writer := editProvider(t, editConfig)
		_, err := provider.EditConfigMapText(context.Background(), editRequest(provider, editConfig, "http://old.example.test:8080/api", newText))
		if editCode(err) != EditNewTextNotPlain || len(writer.patches) != 0 {
			t.Fatalf("new_text %q: err %v", newText, err)
		}
	}
	provider, _, writer := editProvider(t, editConfig)
	_, err := provider.EditConfigMapText(context.Background(), editRequest(provider, editConfig, "  url:", "  password:"))
	if editCode(err) != EditNotShownExactly || len(writer.patches) != 0 {
		t.Fatalf("renaming a shown key to a credential name: err %v", err)
	}
}

// secondReadChanges is a cluster where the ConfigMap is written by someone
// else between the edit's read and the patch's own read.
type secondReadChanges struct {
	*fakeDryRunner
	reads int
}

func (r *secondReadChanges) GetResource(ctx context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	r.reads++
	if r.reads == 2 {
		live := r.live[namespace+"/"+name]
		live.SetResourceVersion("42")
	}
	return r.fakeDryRunner.GetResource(ctx, resource, namespace, name)
}

// A write that lands between the read and the patch fails the patch's
// resourceVersion test: the apiserver applies nothing (test_failed).
func TestAConcurrentWriteFailsTheEditsTest(t *testing.T) {
	provider, runner, writer := editProvider(t, editConfig)
	provider.options.DryRun = &secondReadChanges{fakeDryRunner: runner}
	receipt, err := provider.EditConfigMapText(context.Background(), editRequest(provider, editConfig, "http://old.example.test:8080/api", "http://new.example.test:8080/api"))
	if err != nil || receipt.Outcome != WriteTestFailed || len(writer.patches) != 1 || storedValue(runner) != editConfig {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
}

// The places old_text is tried at are bounded: shown text that a masked
// value repeats past the bound is refused as too common, and nothing is
// written.
func TestOldTextRepeatedPastTheBoundIsRefused(t *testing.T) {
	hidden := strings.Repeat("old.example.test", maxEditCandidates+1)
	value := "database:\n  dsn_password: " + hidden + "\nserver:\n  url: http://old.example.test/\n"
	provider, runner, writer := editProvider(t, value)
	_, err := provider.EditConfigMapText(context.Background(), editRequest(provider, value, "old.example.test", "new.example.test"))
	if editCode(err) != EditNotShownExactly || len(writer.patches) != 0 || storedValue(runner) != value {
		t.Fatalf("err %v", err)
	}
}

// A run configuration that carries a PEM public key among plain settings is
// shown setting by setting: the key and the password masked, the hosts and
// ports readable, and a shown setting editable.
const pemRunConfig = "storage:\n  redis:\n    host: redis.example.test\n    port: 6379\n    password: Hunter9Secret\njwt:\n  enabled: true\n" +
	"  public_key: |\n    -----BEGIN PUBLIC KEY-----\n    MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf\n    AB==\n    -----END PUBLIC KEY-----\n"

func TestAConfigWithAPEMKeyShowsItsPlainSettings(t *testing.T) {
	provider, _, _ := editProvider(t, pemRunConfig)
	masker := &configMasker{fingerprints: provider.newFingerprints()}
	shown := masker.value("runcfg", pemRunConfig)
	for _, kept := range []string{"host: redis.example.test", "port: 6379", "enabled: true"} {
		if !strings.Contains(shown, kept) {
			t.Errorf("lost %q:\n%s", kept, shown)
		}
	}
	for _, hidden := range []string{"BEGIN PUBLIC KEY", "MFwwDQYJKoZIhvcNAQEBBQ", "Hunter9Secret", "AB=="} {
		if strings.Contains(shown, hidden) {
			t.Errorf("%q shown:\n%s", hidden, shown)
		}
	}
	if masker.masked != 2 {
		t.Errorf("masked %d, want the key and the password:\n%s", masker.masked, shown)
	}
	request := editRequest(provider, pemRunConfig, "redis.example.test", "redis-2.example.test")
	receipt, err := provider.EditConfigMapText(context.Background(), request)
	carriesNoSecret(t, receipt, err)
	if err != nil || receipt.Outcome != WriteUpdated {
		t.Fatalf("edit beside a PEM key: receipt %+v err %v", receipt, err)
	}
}

// In text that is not a document, every line of a PEM block is masked, the
// short last line too, and the plain lines around it are shown.
func TestEveryLineOfAPEMBlockInPlainTextIsMasked(t *testing.T) {
	provider, _, _ := editProvider(t, "")
	masker := &configMasker{fingerprints: provider.newFingerprints()}
	text := "upstream mirror.example.test\n-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ2xhdWRlVGVzdENlcnRpZmljYXRlMA\nAB==\n-----END CERTIFICATE-----\ncache 30\n"
	shown := masker.lines(text)
	if !strings.Contains(shown, "upstream mirror.example.test") || !strings.Contains(shown, "cache 30") {
		t.Errorf("plain lines lost:\n%s", shown)
	}
	for _, hidden := range []string{"BEGIN CERTIFICATE", "MIIBszCCAVmg", "AB==", "END CERTIFICATE"} {
		if strings.Contains(shown, hidden) {
			t.Errorf("%q shown:\n%s", hidden, shown)
		}
	}
	if masker.masked != 4 {
		t.Errorf("masked %d lines, want the block's 4:\n%s", masker.masked, shown)
	}
	whole := &configMasker{fingerprints: provider.newFingerprints()}
	if got := whole.value("ca.crt", text[strings.Index(text, "-----BEGIN"):strings.Index(text, "cache")]); !strings.HasPrefix(got, "<redacted bytes=") || whole.masked != 1 {
		t.Errorf("a value that is a PEM block: %q masked %d, want it whole", got, whole.masked)
	}
}
