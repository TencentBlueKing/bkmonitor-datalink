// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package clirelease is the alarmd-cli release this alarmd carries: the CLI
// archives built from the same commit and stamped with the same version, and
// the public routes that hand them out.
//
// The release is embedded in the binary rather than laid beside it in the
// image. One of the image pipelines copies the alarmd binary alone, so the
// binary is the one thing every image is sure to carry.
// A build that did not pack a release -- go build, go test, a developer's
// make -- embeds the placeholder alone and the routes answer
// cli_release_unavailable.
package clirelease

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ManifestFile is the manifest's name under the release root; the archives
// and their .sha256 files sit under <root>/<version>/.
const ManifestFile = "release.json"

// Targets are the platforms a release is built for, named as the admin
// console names its own CLI's so one page and one installer pattern serve both. The
// CLI keeps its profile lock in a Unix-only file lock, so Windows waits until
// that has a Windows counterpart.
var Targets = []Target{
	{Name: "linux-x64", GOOS: "linux", GOARCH: "amd64"},
	{Name: "linux-arm64", GOOS: "linux", GOARCH: "arm64"},
	{Name: "darwin-x64", GOOS: "darwin", GOARCH: "amd64"},
	{Name: "darwin-arm64", GOOS: "darwin", GOARCH: "arm64"},
}

type Target struct {
	Name   string
	GOOS   string
	GOARCH string
}

// Executable is the one file inside each archive.
const Executable = "alarmd-cli"

// maxArtifactBytes bounds what a manifest may claim, as admin's does.
const maxArtifactBytes = 256 << 20

// Manifest is the admin console's release manifest, schema and protocol 1.
type Manifest struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	ProtocolVersion int                 `json:"protocolVersion"`
	Version         string              `json:"version"`
	Artifacts       map[string]Artifact `json:"artifacts"`
}

type Artifact struct {
	File             string `json:"file"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
	ExecutableSize   int64  `json:"executableSize"`
	ExecutableSHA256 string `json:"executableSha256"`
}

// versionPattern is the version alarmd's release script accepts: SemVer,
// with no v prefix and no build metadata, numeric pre-release parts without
// a leading zero.
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)

// ValidVersion reports whether a version can name a release.
func ValidVersion(version string) bool {
	return len(version) <= 128 && versionPattern.MatchString(version)
}

// ArtifactName is the archive's file name for a version and target.
func ArtifactName(version, target string) string {
	return fmt.Sprintf("%s-%s-%s.tar.gz", Executable, version, target)
}

// ChecksumLine is the content of an archive's .sha256 file, the line
// sha256sum -c reads.
func ChecksumLine(sum, file string) string {
	return sum + "  " + file + "\n"
}

var (
	// ErrUnavailable is a binary built without a release.
	ErrUnavailable = errors.New("this alarmd was built without an alarmd-cli release")
	// ErrInvalid is a release that does not belong to this binary or does
	// not hold together: a build defect, never a runtime condition.
	ErrInvalid = errors.New("the embedded alarmd-cli release is invalid")
)

// Release is what a binary carries: a manifest and the files it lists, or the
// reason there is none. It holds the manifest and the checksum lines; the
// archives stay where they are -- in the binary's read-only data, for the
// embedded release -- and are opened per download, so a process that never
// serves one never brings them onto its heap.
type Release struct {
	problem   error
	root      fs.FS
	manifest  Manifest
	document  []byte
	checksums map[string][]byte
	installer []byte
}

//go:embed all:assets
var assets embed.FS

//go:embed install.sh
var installer []byte

// Load reads the release embedded in this binary, for a binary of the given
// version.
func Load(version string) *Release {
	root, err := fs.Sub(assets, "assets")
	if err != nil {
		return &Release{problem: fmt.Errorf("%w: %v", ErrInvalid, err)}
	}
	return LoadFS(root, version)
}

// LoadFS reads a release laid out as Pack writes it, for a binary of the
// given version. Load is LoadFS over what the build embedded.
func LoadFS(root fs.FS, version string) *Release {
	document, err := fs.ReadFile(root, ManifestFile)
	if errors.Is(err, fs.ErrNotExist) {
		return &Release{problem: ErrUnavailable}
	}
	if err != nil {
		return &Release{problem: fmt.Errorf("%w: read %s: %v", ErrInvalid, ManifestFile, err)}
	}
	manifest, err := parseManifest(document)
	if err != nil {
		return &Release{problem: fmt.Errorf("%w: %v", ErrInvalid, err)}
	}
	if manifest.Version != version {
		return &Release{problem: fmt.Errorf("%w: it is release %s and this alarmd is %s", ErrInvalid, manifest.Version, version)}
	}
	checksums := make(map[string][]byte, len(manifest.Artifacts))
	for target, artifact := range manifest.Artifacts {
		// Sizes, not digests, and from Stat: the digests were written by Pack
		// from these bytes and Verify recomputes them at build time. Reading
		// an archive here would copy it onto the heap of every process.
		info, err := fs.Stat(root, path.Join(manifest.Version, artifact.File))
		if err != nil {
			return &Release{problem: fmt.Errorf("%w: %s: %v", ErrInvalid, target, err)}
		}
		if !info.Mode().IsRegular() || info.Size() != artifact.Size {
			return &Release{problem: fmt.Errorf("%w: %s is %d bytes, the manifest says %d", ErrInvalid, artifact.File, info.Size(), artifact.Size)}
		}
		checksum, err := fs.ReadFile(root, path.Join(manifest.Version, artifact.File+".sha256"))
		if err != nil {
			return &Release{problem: fmt.Errorf("%w: %s: %v", ErrInvalid, target, err)}
		}
		if string(checksum) != ChecksumLine(artifact.SHA256, artifact.File) {
			return &Release{problem: fmt.Errorf("%w: %s.sha256 does not match the manifest", ErrInvalid, artifact.File)}
		}
		checksums[artifact.File+".sha256"] = checksum
	}
	return &Release{root: root, manifest: manifest, document: document, checksums: checksums, installer: installer}
}

func parseManifest(document []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %v", ManifestFile, err)
	}
	if manifest.SchemaVersion != 1 || manifest.ProtocolVersion != 1 {
		return Manifest{}, fmt.Errorf("%s is schema %d protocol %d, want 1 and 1", ManifestFile, manifest.SchemaVersion, manifest.ProtocolVersion)
	}
	if !ValidVersion(manifest.Version) {
		return Manifest{}, fmt.Errorf("%s names version %q", ManifestFile, manifest.Version)
	}
	if len(manifest.Artifacts) == 0 {
		return Manifest{}, fmt.Errorf("%s lists no artifact", ManifestFile)
	}
	known := make(map[string]bool, len(Targets))
	for _, target := range Targets {
		known[target.Name] = true
	}
	for target, artifact := range manifest.Artifacts {
		switch {
		case !known[target]:
			return Manifest{}, fmt.Errorf("%s lists unknown target %q", ManifestFile, target)
		case artifact.File != ArtifactName(manifest.Version, target):
			return Manifest{}, fmt.Errorf("%s names %q for %s", ManifestFile, artifact.File, target)
		case !isDigest(artifact.SHA256) || !isDigest(artifact.ExecutableSHA256):
			return Manifest{}, fmt.Errorf("%s has a malformed digest for %s", ManifestFile, target)
		case artifact.Size < 1 || artifact.Size > maxArtifactBytes || artifact.ExecutableSize < 1 || artifact.ExecutableSize > maxArtifactBytes:
			return Manifest{}, fmt.Errorf("%s has an out-of-range size for %s", ManifestFile, target)
		}
	}
	return manifest, nil
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Problem is nil for a usable release, else wraps ErrUnavailable or
// ErrInvalid. A nil Release is unavailable.
func (r *Release) Problem() error {
	if r == nil {
		return ErrUnavailable
	}
	return r.problem
}

// Version is the release's version, empty when there is no usable release.
func (r *Release) Version() string {
	if r.Problem() != nil {
		return ""
	}
	return r.manifest.Version
}

// TargetNames lists the release's targets in order.
func (r *Release) TargetNames() []string {
	if r.Problem() != nil {
		return nil
	}
	names := make([]string, 0, len(r.manifest.Artifacts))
	for name := range r.manifest.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Verify recomputes every digest the manifest records, the archive's and the
// executable's inside it. It is the build-time check (alarmd
// --check-cli-release); serving relies on it having passed.
func (r *Release) Verify() error {
	if err := r.Problem(); err != nil {
		return err
	}
	for target, artifact := range r.manifest.Artifacts {
		content, err := fs.ReadFile(r.root, path.Join(r.manifest.Version, artifact.File))
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalid, artifact.File, err)
		}
		if digest(content) != artifact.SHA256 {
			return fmt.Errorf("%w: %s does not hash to its manifest digest", ErrInvalid, artifact.File)
		}
		executable, err := ReadExecutable(content)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalid, target, err)
		}
		if int64(len(executable)) != artifact.ExecutableSize || digest(executable) != artifact.ExecutableSHA256 {
			return fmt.Errorf("%w: the executable in %s does not match the manifest", ErrInvalid, artifact.File)
		}
	}
	return nil
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
