// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
)

// A snapshot is read with the same commands as the deployment: discover,
// describe, invoke and diagnose take --snapshot <file> and answer from it,
// in the same shapes, with meta.snapshot naming the file and when it was
// collected. A read the snapshot does not hold is not_in_snapshot; nothing
// goes to the network.

// snapshotReadSlack is what an archive holds beyond its answers: the
// manifest, the collector's log, the sums and the tar headers. The answers
// themselves are bounded by snapshotMaxBytes, which the collector counts.
const snapshotReadSlack = 8 << 20

type snapshotArchive struct {
	file     string
	manifest SnapshotManifest
	records  map[string]map[string]any
}

func openSnapshot(file string) (*snapshotArchive, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, errors.New("cannot open the snapshot file")
	}
	defer f.Close()
	zipped, err := gzip.NewReader(f)
	if err != nil {
		return nil, errors.New("the snapshot file is not a gzip archive")
	}
	archive := &snapshotArchive{file: file, records: map[string]map[string]any{}}
	tarred := tar.NewReader(io.LimitReader(zipped, int64(snapshotMaxBytes+snapshotReadSlack)))
	manifestSeen := false
	for {
		header, err := tarred.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("the snapshot archive is unreadable or past its bound")
		}
		_, name, _ := strings.Cut(header.Name, "/")
		data, err := io.ReadAll(tarred)
		if err != nil {
			return nil, errors.New("the snapshot archive is unreadable or past its bound")
		}
		switch {
		case name == "manifest.json":
			if json.Unmarshal(data, &archive.manifest) != nil || archive.manifest.Format != snapshotFormat {
				return nil, errors.New("the snapshot's manifest is not one this CLI reads")
			}
			manifestSeen = true
		case strings.HasPrefix(name, "ops/") && strings.HasSuffix(name, ".json"):
			var record map[string]any
			if json.Unmarshal(data, &record) != nil {
				return nil, errors.New("the snapshot's " + name + " is not readable JSON")
			}
			archive.records[name] = record
		}
	}
	if !manifestSeen {
		return nil, errors.New("the snapshot has no manifest")
	}
	return archive, nil
}

// answer is a stored record as the command prints it: without the request
// it was filed with, and with meta.snapshot.
func (archive *snapshotArchive) answer(record map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range record {
		if key != "request" {
			out[key] = value
		}
	}
	meta := objectField(out, "meta")
	if meta == nil {
		meta = map[string]any{}
		out["meta"] = meta
	}
	meta["snapshot"] = map[string]any{"file": archive.file, "collected_at": archive.manifest.StartedAt, "environment_id": archive.manifest.EnvironmentID}
	return out
}

// find is the record of an operation asked with exactly these params.
func (archive *snapshotArchive) find(operation string, params map[string]any) (map[string]any, []any) {
	var asked []any
	names := make([]string, 0, len(archive.records))
	for name := range archive.records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		record := archive.records[name]
		request := objectField(record, "request")
		if stringField(request, "operation") != operation || stringField(request, "mode") != "invoke" {
			continue
		}
		stored := objectField(request, "params")
		if stored == nil {
			stored = map[string]any{}
		}
		if reflect.DeepEqual(normalized(stored), normalized(params)) {
			return record, nil
		}
		asked = append(asked, stored)
	}
	return nil, asked
}

// normalized is params as JSON reads them back, so numbers compare equal
// whichever way they were written.
func normalized(params map[string]any) map[string]any {
	data, _ := json.Marshal(params)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func (a *App) fromSnapshot(command, operation string, params map[string]any, o options) int {
	archive, err := openSnapshot(o.snapshot)
	if err != nil {
		return a.fail("snapshot_unreadable", err.Error(), 1)
	}
	if o.env != "" && o.env != archive.manifest.EnvironmentID {
		return a.fail("invalid_input", "the snapshot is of environment "+archive.manifest.EnvironmentID+", not "+o.env, 2)
	}
	notIn := func(message string, asked []any) int {
		out := map[string]any{"status": "error", "summary": message, "error": map[string]any{"code": "not_in_snapshot", "message": message},
			"result": map[string]any{"asked_in_snapshot": asked}, "next_call": []any{}}
		return a.emitResponse(archive.answer(out), nil, http.StatusOK)
	}
	switch command {
	case "discover":
		if record := archive.records["ops/discover.json"]; record != nil {
			return a.emitResponse(archive.answer(record), nil, http.StatusOK)
		}
		return notIn("the snapshot holds no catalog", nil)
	case "describe":
		if record := archive.records["ops/describe/"+operation+".json"]; record != nil {
			return a.emitResponse(archive.answer(record), nil, http.StatusOK)
		}
		return notIn("the snapshot holds no description of "+operation, nil)
	case "invoke":
		record, asked := archive.find(operation, params)
		if record != nil {
			return a.emitResponse(archive.answer(record), nil, http.StatusOK)
		}
		if asked == nil {
			asked = []any{}
		}
		return notIn("the snapshot did not read "+operation+" with these params; asked_in_snapshot lists the params it was read with", asked)
	case "diagnose":
		fetch := func(cursor string) (map[string]any, int, error) {
			params := map[string]any{}
			if cursor != "" {
				params["cursor"] = cursor
			}
			record, _ := archive.find(diagnoseOperation, params)
			if record == nil {
				return map[string]any{"status": "error", "error": map[string]any{"code": "not_in_snapshot", "message": "the snapshot stops before this page"}}, http.StatusOK, nil
			}
			return record, http.StatusOK, nil
		}
		run, err := a.diagnosisPagesFrom(fetch)
		if err != nil {
			return a.fail("snapshot_unreadable", err.Error(), 1)
		}
		if run.failed != nil {
			return a.emitResponse(archive.answer(run.failed), nil, http.StatusOK)
		}
		return a.emitResponse(archive.answer(run.result()), nil, http.StatusOK)
	}
	return a.fail("invalid_input", "--snapshot is valid with discover, describe, invoke and diagnose", 2)
}
