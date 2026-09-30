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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Profile struct {
	EnvironmentID   string `json:"environment_id"`
	EnvironmentName string `json:"environment_name"`
	PublicBaseURL   string `json:"public_base_url"`
	AccessToken     string `json:"access_token,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	Scope           string `json:"scope,omitempty"`
	CACert          string `json:"ca_cert,omitempty"`
	InsecureTLS     bool   `json:"insecure_tls,omitempty"`
	// RefreshToken is the pairing's renewal credential: spent for a new
	// session and the next credential whenever the session runs out.
	RefreshToken string `json:"refresh_token,omitempty"`
	PairingID    string `json:"pairing_id,omitempty"`
	// Pairing is what the server said about pairing this session: paired,
	// pairing_limit_reached, not_offered (a server that does not pair) or
	// upgrade_refused. Empty is a session from before this client, which is
	// offered for pairing once.
	Pairing string `json:"pairing,omitempty"`
}

type config struct {
	DefaultEnvironment string             `json:"default_environment,omitempty"`
	Profiles           map[string]Profile `json:"profiles"`
}

type Store struct{ Dir string }

func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("configuration directory must be a real directory")
	}
	return os.Chmod(dir, 0700)
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("configuration path must be a regular file")
	}
	return os.Chmod(path, 0600)
}

func (s Store) locked(fn func(*config) (bool, error)) error {
	if err := secureDir(s.Dir); err != nil {
		return err
	}
	lockPath := filepath.Join(s.Dir, ".lock")
	if err := regularFile(lockPath); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return err
	}
	defer unlockFile(f)
	path := filepath.Join(s.Dir, "profiles.json")
	if err := regularFile(path); err != nil {
		return err
	}
	c := config{Profiles: map[string]Profile{}}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &c); err != nil || c.Profiles == nil {
			return errors.New("invalid profiles.json; restore it before continuing")
		}
	}
	changed, err := fn(&c)
	if err != nil || !changed {
		return err
	}
	data, err = json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".alarmd-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (s Store) get(env string) (Profile, error) {
	var p Profile
	err := s.locked(func(c *config) (bool, error) {
		var ok bool
		p, ok = c.Profiles[env]
		if !ok || (p.AccessToken == "" && p.RefreshToken == "") {
			return false, fmt.Errorf("environment has no session; open the deployment's login page (<entry>/cli) and run the auth listen command it shows, or run: alarmd-cli auth login --env %s", env)
		}
		return false, nil
	})
	return p, err
}

// profile is the environment's binding, with or without a session: what
// auth listen --env needs to know where the page lives.
func (s Store) profile(env string) (Profile, error) {
	var p Profile
	err := s.locked(func(c *config) (bool, error) {
		var ok bool
		p, ok = c.Profiles[env]
		if !ok {
			return false, errors.New("environment has no profile; use auth listen --url with the entry the authorization page shows")
		}
		return false, nil
	})
	return p, err
}

func checkBinding(c *config, p Profile, rebind bool) error {
	old, exists := c.Profiles[p.EnvironmentID]
	if !exists || rebind {
		return nil
	}
	a, err := baseURL(old.PublicBaseURL)
	if err != nil {
		return errors.New("stored environment URL is invalid")
	}
	b, err := baseURL(p.PublicBaseURL)
	if err != nil {
		return err
	}
	if a.Scheme != b.Scheme || a.Host != b.Host {
		return errors.New("environment origin changed; verify the new entry and use auth login --rebind")
	}
	return nil
}

func (s Store) checkBinding(p Profile, rebind bool) error {
	return s.locked(func(c *config) (bool, error) { return false, checkBinding(c, p, rebind) })
}

func (s Store) save(p Profile, rebind bool) error {
	return s.locked(func(c *config) (bool, error) {
		if err := checkBinding(c, p, rebind); err != nil {
			return false, err
		}
		c.Profiles[p.EnvironmentID] = p
		return true, nil
	})
}

func sameSession(a, b Profile) bool {
	return a.SessionID != "" && a.SessionID == b.SessionID && sha256.Sum256([]byte(a.AccessToken)) == sha256.Sum256([]byte(b.AccessToken))
}

// CAS guards both against late replies replacing a new login and late replies
// resurrecting credentials removed by another process.
func (s Store) updateExpiry(expected Profile, expiry string) error {
	t, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		return errors.New("invalid session expiry")
	}
	return s.locked(func(c *config) (bool, error) {
		current, ok := c.Profiles[expected.EnvironmentID]
		if !ok || !sameSession(current, expected) {
			return false, nil
		}
		old, _ := time.Parse(time.RFC3339, current.ExpiresAt)
		if !t.After(old) {
			return false, nil
		}
		current.ExpiresAt = expiry
		c.Profiles[current.EnvironmentID] = current
		return true, nil
	})
}

func (s Store) clear(expected Profile) (bool, error) {
	cleared := false
	err := s.locked(func(c *config) (bool, error) {
		current, ok := c.Profiles[expected.EnvironmentID]
		if !ok || !sameSession(current, expected) {
			return false, nil
		}
		current.AccessToken, current.SessionID, current.ExpiresAt, current.Scope = "", "", "", ""
		current.RefreshToken, current.PairingID, current.Pairing = "", "", ""
		c.Profiles[current.EnvironmentID] = current
		cleared = true
		return true, nil
	})
	return cleared, err
}

func (s Store) list() (map[string]any, error) {
	result := map[string]any{}
	err := s.locked(func(c *config) (bool, error) {
		profiles := map[string]any{}
		for id, p := range c.Profiles {
			profiles[id] = map[string]any{"environment_id": p.EnvironmentID, "environment_name": p.EnvironmentName, "public_base_url": p.PublicBaseURL, "session_id": p.SessionID, "expires_at": p.ExpiresAt, "authenticated": p.AccessToken != "", "paired": p.RefreshToken != "", "ca_cert": p.CACert, "insecure_tls": p.InsecureTLS}
		}
		result["profiles"], result["default_environment"] = profiles, c.DefaultEnvironment
		return false, nil
	})
	return result, err
}

func (s Store) use(env string) error {
	return s.locked(func(c *config) (bool, error) {
		if _, ok := c.Profiles[env]; !ok {
			return false, fmt.Errorf("environment profile does not exist")
		}
		c.DefaultEnvironment = env
		return true, nil
	})
}
