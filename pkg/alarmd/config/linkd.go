package config

import (
	"errors"
	"net/url"
	"os"
	"time"
)

// LinkdConfig is the alert link as this deployment reaches it: the Console's
// address and its credentials, and nothing else. The link's target - its
// source, hook, prefix, where its sets are and which sources share them - is
// read from the Console, which lists exactly one target for a link alarmd
// reads; nothing is read until the Console names the place.
//
// Connection and KeyPrefix are where discovery found the sets, written back
// once the Console has named them, and are never decoded from the file.
type LinkdConfig struct {
	Connection *RedisConnectionConfig `yaml:"-"`
	KeyPrefix  string                 `yaml:"-"`
	ConsoleURL string                 `yaml:"console_url"`
	Username   string                 `yaml:"username"`
	Password   string                 `yaml:"password"`
}

// LinkdCalibrationInterval is how often the copy calibrates against the
// link's full sets, and asks the Console again how they are keyed. A
// program constant: the deployment knows no better than this how often that
// should be.
const LinkdCalibrationInterval = 30 * time.Minute

// The Console's Basic Auth may come from the environment instead of the file,
// so that a deployment can hand alarmd the same Secret the alert link's own
// Console is given - its chart keeps the credentials in an existing Secret -
// rather than restating them in alarmd's configuration.
const (
	LinkdConsoleUsernameEnvironment = "ALARMD_LINKD_CONSOLE_USERNAME"
	LinkdConsolePasswordEnvironment = "ALARMD_LINKD_CONSOLE_PASSWORD"
)

// resolveCredentialsFromEnvironment fills the Console credentials from the
// environment. A credential stated in both places is refused: two sources for
// one secret is a deployment that can rotate one and keep using the other.
func (c *LinkdConfig) resolveCredentialsFromEnvironment() error {
	for _, field := range []struct {
		name  string
		value *string
	}{{LinkdConsoleUsernameEnvironment, &c.Username}, {LinkdConsolePasswordEnvironment, &c.Password}} {
		env, ok := os.LookupEnv(field.name)
		if !ok || env == "" {
			continue
		}
		if *field.value != "" {
			return errors.New("linkd console credentials are set both in the file and in " + field.name)
		}
		*field.value = env
	}
	return nil
}

func (c LinkdConfig) Prefix() string {
	if c.KeyPrefix == "" {
		return "alarmd:open_alerts"
	}
	return c.KeyPrefix
}

func (c LinkdConfig) Validate() error {
	if c.ConsoleURL == "" {
		if c.Username != "" || c.Password != "" {
			return errors.New("linkd console_url is required for credentials")
		}
		return nil
	}
	u, err := url.Parse(c.ConsoleURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("linkd console_url must be an HTTP service URL without credentials or query")
	}
	if c.Username == "" || c.Password == "" {
		return errors.New("linkd console service requires username and password")
	}
	return nil
}

type LinkdCapacity struct{ Bytes, Strategies, Members, LocalEntries, ReadBatch, GroupBatch, CloseBatch int }

func DeriveLinkdCapacity(in CapacityInputs) LinkdCapacity {
	// The index and calibration metadata receive one percent of container
	// memory. Local ACK/owner bookkeeping and the legacy calendar cache have
	// separate derived limits. These admission estimates are not an RSS cap.
	bytes := int(min(in.MemoryLimitBytes/100, uint64(^uint(0)>>1)))
	if bytes < 64<<10 {
		return LinkdCapacity{}
	}
	ops := max(1, min(in.CPUBudget*4, bytes/4096))
	return LinkdCapacity{Bytes: bytes, Strategies: bytes / 2048, Members: bytes / 512,
		LocalEntries: bytes / 2048, ReadBatch: ops, GroupBatch: ops, CloseBatch: min(64, ops*4)}
}
