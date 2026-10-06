package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"
)

// LinkdConfig is the alert link as this deployment reaches it. The Console
// address and its credentials are the whole of it in the usual case: which of
// the link's targets is this deployment's - its source, hook, prefix, where
// its sets are and which sources share them - is read from the Console.
// EventSourceID and HookName only narrow that choice when the link maintains
// more than one target. An omitted connection and prefix are where this
// process reads the sets from, its runtime Redis and alarmd:open_alerts, and
// the Console is asked whether that is where the link writes them.
type LinkdConfig struct {
	Connection        *RedisConnectionConfig `yaml:"connection"`
	KeyPrefix         string                 `yaml:"key_prefix"`
	ConsoleURL        string                 `yaml:"console_url"`
	EventSourceID     string                 `yaml:"event_source_id"`
	HookName          string                 `yaml:"hook_name"`
	Username          string                 `yaml:"username"`
	Password          string                 `yaml:"password"`
	ReconcileInterval Duration               `yaml:"reconcile_interval"`
	// AbsentCloseSend arms the close for strategies that no longer exist.
	// False, the default, takes the difference and reports every reading
	// without sending one close: the strategies it would have closed are
	// counted, the alerts are not.
	//
	// It is a setting rather than a program constant because what it decides
	// is not a fact about the program. The difference's gates are ours to
	// get right; whether this deployment's numbers - how many strategies it
	// let go, how many of those still hold alerts - are the numbers whoever
	// runs it expects, is theirs, and nothing inside the process can answer
	// it. A capability that makes alerts disappear is armed once those
	// numbers have been read, not once the code is believed.
	AbsentCloseSend bool `yaml:"absent_close_send"`
}

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

func (c LinkdConfig) CalibrationInterval() time.Duration {
	if c.ReconcileInterval == 0 {
		return 30 * time.Minute
	}
	return c.ReconcileInterval.Duration()
}

func (c LinkdConfig) Validate() error {
	if strings.TrimSpace(c.Prefix()) != c.Prefix() || strings.ContainsAny(c.Prefix(), "\r\n\x00") {
		return errors.New("linkd key_prefix is invalid")
	}
	if c.CalibrationInterval() < time.Minute {
		return errors.New("linkd reconcile_interval must be at least one minute")
	}
	if c.Connection != nil {
		if err := c.Connection.validate("phase_two.linkd.connection"); err != nil {
			return err
		}
	}
	if c.ConsoleURL == "" {
		if c.EventSourceID != "" || c.HookName != "" || c.Username != "" || c.Password != "" {
			return errors.New("linkd console_url is required for source binding and credentials")
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
