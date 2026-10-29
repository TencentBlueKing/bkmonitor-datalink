// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package clirelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Placeholder is the one committed file under the release root. Pack keeps
// it, so the embed pattern always matches something and a build without a
// release still compiles.
const Placeholder = "README.md"

// archiveTime is every archive entry's modification time. A fixed time, owner
// and mode make the archive a function of the executable alone, so the amd64
// and arm64 image builds of one version hand out the same bytes and the same
// digest whichever replica answers.
var archiveTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Build writes the CLI executable for a target to dst.
type Build func(target Target, dst string) error

// Pack builds the CLI for every target and writes the release under root:
// root/release.json and root/<version>/<archive>{,.sha256}. Whatever an
// earlier Pack left under root is removed first, the placeholder aside, so a
// stale release is never embedded beside a fresh one.
func Pack(root, version string, build Build) error {
	if !ValidVersion(version) {
		return fmt.Errorf("version %q is not a release version: set VERSION to the SemVer the alarmd binary is stamped with, or skip the CLI release (CLI_RELEASE=0)", version)
	}
	if err := clearRoot(root); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "alarmd-cli-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err := os.MkdirAll(filepath.Join(root, version), 0o755); err != nil {
		return err
	}
	manifest := Manifest{SchemaVersion: 1, ProtocolVersion: 1, Version: version, Artifacts: map[string]Artifact{}}
	for _, target := range Targets {
		built := filepath.Join(scratch, target.Name)
		if err := build(target, built); err != nil {
			return fmt.Errorf("build alarmd-cli for %s: %w", target.Name, err)
		}
		executable, err := os.ReadFile(built)
		if err != nil {
			return err
		}
		archive, err := archiveOf(executable)
		if err != nil {
			return err
		}
		artifact := Artifact{
			File: ArtifactName(version, target.Name), Size: int64(len(archive)), SHA256: digest(archive),
			ExecutableSize: int64(len(executable)), ExecutableSHA256: digest(executable),
		}
		if err := os.WriteFile(filepath.Join(root, version, artifact.File), archive, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, version, artifact.File+".sha256"), []byte(ChecksumLine(artifact.SHA256, artifact.File)), 0o644); err != nil {
			return err
		}
		manifest.Artifacts[target.Name] = artifact
	}
	document, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ManifestFile), append(document, '\n'), 0o644)
}

func clearRoot(root string) error {
	// Everything under the root but the placeholder is removed, so a root
	// without one -- a mistyped -out such as "." -- is refused rather than
	// emptied.
	if _, err := os.Stat(filepath.Join(root, Placeholder)); err != nil {
		return fmt.Errorf("%s is not a release root: it has no %s placeholder", root, Placeholder)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == Placeholder {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func archiveOf(executable []byte) ([]byte, error) {
	var buffer bytes.Buffer
	compressor, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	archive := tar.NewWriter(compressor)
	header := &tar.Header{
		Typeflag: tar.TypeReg, Name: Executable, Mode: 0o755, Size: int64(len(executable)),
		ModTime: archiveTime, Format: tar.FormatUSTAR,
	}
	if err := archive.WriteHeader(header); err != nil {
		return nil, err
	}
	if _, err := archive.Write(executable); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := compressor.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// ReadExecutable returns the executable an archive carries, refusing an
// archive that carries anything else.
func ReadExecutable(archive []byte) ([]byte, error) {
	decompressor, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(decompressor)
	header, err := reader.Next()
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	if header.Typeflag != tar.TypeReg || header.Name != Executable || header.Size < 1 || header.Size > maxArtifactBytes {
		return nil, fmt.Errorf("archive holds %q, want the %s executable alone", header.Name, Executable)
	}
	executable, err := io.ReadAll(io.LimitReader(reader, header.Size))
	if err != nil {
		return nil, err
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("archive holds more than the %s executable", Executable)
	}
	return executable, nil
}
