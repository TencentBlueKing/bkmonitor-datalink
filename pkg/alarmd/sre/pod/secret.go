// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"regexp"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// OpReadSecret is the Secret read, as a receipt and the audit name it.
const OpReadSecret = "secret.get"

// MaxSecretKeys bounds the keys one read returns.
const MaxSecretKeys = 1024

// SecretReader is the one Kubernetes read a Secret read makes: a get by
// name. The ServiceAccount may get only the Secrets the deployment's
// whitelist names, so a Secret outside it is the apiserver's refusal.
type SecretReader interface {
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)
}

// SecretRequest names one Secret. A read never returns a value.
type SecretRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// SecretKey is one key of a Secret: its value's size and fingerprint, never
// the value, masked or otherwise.
type SecretKey struct {
	Key         string `json:"key"`
	Bytes       int    `json:"bytes"`
	Fingerprint string `json:"fingerprint"`
}

// SecretReceipt is what one Secret read returned: never a value, each key
// as its size and fingerprint. Truncated says keys past MaxSecretKeys were
// left out.
type SecretReceipt struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
	Type      string `json:"type"`
	// Labels, Annotations, OwnerReferences and CreatedAt say what owns the
	// Secret - Helm's ownership marks among them - so a reader can tell
	// whether removing a release deletes it. Annotations are cleaned as a
	// resource read cleans them: the last-applied copy, which holds the
	// values, dropped, and a credential-named one masked.
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	OwnerReferences []SecretOwner     `json:"owner_references,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	Keys            []SecretKey       `json:"keys"`
	// FingerprintScope is where the fingerprints compare: "deployment" or,
	// when this process had to make its own key, "process".
	FingerprintScope  string    `json:"fingerprint_scope"`
	Truncated         bool      `json:"truncated"`
	RedactionContract string    `json:"redaction_contract"`
	At                time.Time `json:"at"`
}

// SecretOwner is one owner reference of a Secret.
type SecretOwner struct {
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

// ReadSecret reads one Secret the deployment's whitelist names. A value is
// returned only when the request reveals it, and then masked as a ConfigMap
// read masks it: whole under a credential-named key, otherwise through the
// output redaction (credentials the deployment knows, credential fields, URL
// userinfo).
func (p *Provider) ReadSecret(ctx context.Context, request SecretRequest) (SecretReceipt, error) {
	fingerprints := p.newFingerprints()
	result := SecretReceipt{Namespace: request.Namespace, Name: request.Name, Keys: []SecretKey{}, FingerprintScope: fingerprints.Scope(), RedactionContract: RedactionContract, At: p.options.Now().UTC()}
	if p.options.Secrets == nil {
		return result, &Error{Code: CodeAPIError, Message: "Kubernetes Secret adapter is unavailable"}
	}
	namespace, err := p.namespace(request.Namespace)
	if err != nil {
		return result, err
	}
	result.Namespace = namespace
	if len(validation.IsDNS1123Subdomain(request.Name)) != 0 {
		return result, invalid("name must be a Secret name")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	secret, err := p.options.Secrets.GetSecret(ctx, namespace, request.Name)
	if err != nil {
		return result, apiError(err)
	}
	result.UID, result.Type = string(secret.UID), string(secret.Type)
	result.CreatedAt = secret.CreationTimestamp.UTC()
	if len(secret.Labels) > 0 {
		result.Labels = map[string]string{}
		for key, value := range secret.Labels {
			redacted, _ := p.redactor.bounded(value, p.options.MaxOutputBytes)
			result.Labels[key] = redacted
		}
	}
	for key, value := range secret.Annotations {
		if key == lastAppliedAnnotation {
			continue
		}
		if result.Annotations == nil {
			result.Annotations = map[string]string{}
		}
		if value != "" && credentialKey.MatchString(key) {
			result.Annotations[key] = "<redacted>"
			continue
		}
		redacted, _ := p.redactor.bounded(value, p.options.MaxOutputBytes)
		result.Annotations[key] = redacted
	}
	for _, owner := range secret.OwnerReferences {
		result.OwnerReferences = append(result.OwnerReferences, SecretOwner{APIVersion: owner.APIVersion, Kind: owner.Kind, Name: owner.Name, UID: string(owner.UID)})
	}
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > MaxSecretKeys {
		keys, result.Truncated = keys[:MaxSecretKeys], true
	}
	for _, key := range keys {
		value := secret.Data[key]
		result.Keys = append(result.Keys, SecretKey{Key: key, Bytes: len(value), Fingerprint: fingerprints.sum(value)})
	}
	return result, nil
}

// OpReadConfigMap is the ConfigMap read, as a receipt and the audit name it.
const OpReadConfigMap = "configmap.get"

// ConfigMapReader is the one Kubernetes read a ConfigMap read makes.
type ConfigMapReader interface {
	GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error)
}

// ConfigMapRequest names one ConfigMap.
type ConfigMapRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ConfigMapKey is one key of a ConfigMap: the size and fingerprint of its
// value as stored, and the value as it may be shown (ConfigValueContract):
// plain configuration in clear, everything else masked as its size and
// fingerprint; Masked counts the masked parts.
type ConfigMapKey struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
	Value string `json:"value"`
	// Fingerprint is the whole value's, as a Secret value's is, so it can be
	// compared with the Secret that should hold it.
	Fingerprint string `json:"fingerprint"`
	Masked      int    `json:"masked,omitempty"`
}

// ConfigMapReceipt is what one ConfigMap read returned. Values are always
// redacted - modules render connection strings and passwords into ConfigMaps
// in plain text - and the whole answer is bounded by the output budget;
// Truncated says a value or keys were cut.
type ConfigMapReceipt struct {
	Namespace         string         `json:"namespace"`
	Name              string         `json:"name"`
	UID               string         `json:"uid"`
	Keys              []ConfigMapKey `json:"keys"`
	BinaryKeys        []string       `json:"binary_keys,omitempty"`
	Truncated         bool           `json:"truncated"`
	RedactionContract string         `json:"redaction_contract"`
	ValueContract     string         `json:"value_contract"`
	At                time.Time      `json:"at"`
}

// credentialKey is a ConfigMap key whose value is a credential by its name -
// REDIS_PASSWORD, APP_SECRET, API_TOKEN - stored with nothing around it for
// the text redaction to recognize, so the whole value is masked.
var credentialKey = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|private[_-]?key|credential|access[_-]?key|auth)`)

// ReadConfigMap reads one ConfigMap in the namespace the request names, this
// process's own by default: each key with its value's size and fingerprint,
// and the value shown only where it is plain configuration
// (ConfigValueContract), then through the output redaction.
func (p *Provider) ReadConfigMap(ctx context.Context, request ConfigMapRequest) (ConfigMapReceipt, error) {
	result := ConfigMapReceipt{Namespace: request.Namespace, Name: request.Name, Keys: []ConfigMapKey{}, RedactionContract: RedactionContract,
		ValueContract: ConfigValueContract, At: p.options.Now().UTC()}
	if p.options.ConfigMaps == nil {
		return result, &Error{Code: CodeAPIError, Message: "Kubernetes ConfigMap adapter is unavailable"}
	}
	namespace, err := p.namespace(request.Namespace)
	if err != nil {
		return result, err
	}
	result.Namespace = namespace
	if len(validation.IsDNS1123Subdomain(request.Name)) != 0 {
		return result, invalid("name must be a ConfigMap name")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	configMap, err := p.options.ConfigMaps.GetConfigMap(ctx, namespace, request.Name)
	if err != nil {
		return result, apiError(err)
	}
	result.UID = string(configMap.UID)
	keys := make([]string, 0, len(configMap.Data))
	for key := range configMap.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > MaxSecretKeys {
		keys, result.Truncated = keys[:MaxSecretKeys], true
	}
	remaining := p.options.MaxOutputBytes
	fingerprints := p.newFingerprints()
	for _, key := range keys {
		value := configMap.Data[key]
		masker := &configMasker{fingerprints: fingerprints}
		shown := masker.value(key, value)
		redacted, cut := p.redactor.bounded(shown, max(remaining, 0))
		remaining -= int64(len(redacted))
		result.Truncated = result.Truncated || cut
		result.Keys = append(result.Keys, ConfigMapKey{Key: key, Bytes: len(value), Value: redacted,
			Fingerprint: fingerprints.sum([]byte(value)), Masked: masker.masked})
	}
	for key := range configMap.BinaryData {
		result.BinaryKeys = append(result.BinaryKeys, key)
	}
	sort.Strings(result.BinaryKeys)
	return result, nil
}
