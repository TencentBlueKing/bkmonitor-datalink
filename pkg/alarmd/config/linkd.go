package config

import (
	"errors"
	"net/url"
	"strings"
)

// LinkdConfig is the alert link as this deployment reaches it: its control
// plane, whose management API alarmd reads and closes the link's alerts
// through. Nothing else of the link is read: not its sets, not a Console.
type LinkdConfig struct {
	// ControlPlane is the link's control plane: the recovery gate's copy is
	// refreshed through its search (activecopy), and every close goes
	// through its filter (filterclose). A Control or Worker of a deployment
	// that states the link (Present) does not load without it
	// (ErrLinkdControlPlaneRequired). A deployment that does not state the
	// link reads and closes nothing there, and its recovery gate does not
	// apply.
	ControlPlane LinkdControlPlaneConfig `yaml:"control_plane"`
	// Declared is whether the configuration file has a phase_two.linkd key,
	// set by the decoder (decodeDocument); a configuration built in code
	// states the link with Present's other half, a control plane field.
	Declared bool `yaml:"-" json:"-"`
}

// Present says the link is this deployment's alert destination: the
// configuration has a phase_two.linkd block, or names any part of its
// control plane. It is keyed on the destination and never on the output
// protocol: on a deployment without the link the standard raw event feeds
// the platform's own alert pipeline, and the wire format cannot say whether
// the link is in use.
func (c LinkdConfig) Present() bool {
	plane := c.ControlPlane
	return c.Declared || plane.URL != "" || plane.JWTSecretFile != "" || plane.JWTSecret != "" ||
		plane.InlineJWTSecret != "" || plane.JWTUsername != "" || plane.EventSourceID != ""
}

// LinkdControlPlaneConfig is the link's control plane as alarmd reaches it:
// the API's address and the credential its management routes take - an
// Internal-Token signed with the link's dispatch JWT secret, which is given
// by file only (JWTSecretFile) and never written in the file.
type LinkdControlPlaneConfig struct {
	// URL is the link control plane's address, without its /api/v1 routes.
	URL           string `yaml:"url"`
	JWTSecretFile string `yaml:"jwt_secret_file"`
	// JWTSecret is resolved from JWTSecretFile (resolveCredentials).
	JWTSecret string `yaml:"-" json:"-"`
	// InlineJWTSecret is only here to be refused: the secret is given by
	// file, never written in the configuration.
	InlineJWTSecret string `yaml:"jwt_secret" json:"-"`
	// JWTUsername is the caller's identity in the token; alarmd when unset.
	JWTUsername string `yaml:"jwt_username"`
	// EventSourceID is the link's source alarmd writes to; built_in_bk when
	// unset. Every filter alarmd sends names it: the link scopes a search
	// to the tenant only.
	EventSourceID string `yaml:"event_source_id"`
}

// Configured says the deployment names the link's control plane and its
// secret. It is where the link is, not a switch: whether its closes are
// used is the link's answer.
func (c LinkdControlPlaneConfig) Configured() bool { return c.URL != "" && c.JWTSecret != "" }

// Username is the token's caller identity.
func (c LinkdControlPlaneConfig) Username() string {
	if c.JWTUsername == "" {
		return "alarmd"
	}
	return c.JWTUsername
}

// Source is the link's source alarmd's events arrive under.
func (c LinkdControlPlaneConfig) Source() string {
	if c.EventSourceID == "" {
		return "built_in_bk"
	}
	return c.EventSourceID
}

func (c LinkdControlPlaneConfig) validate() error {
	if c.InlineJWTSecret != "" {
		return errors.New("linkd control_plane jwt_secret is given by jwt_secret_file only")
	}
	if c.URL == "" {
		return nil
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("linkd control_plane url must be an HTTP service URL without credentials or query")
	}
	// No secret is not refused here: a deployment that states the link is
	// refused without one by validateDestination.
	for _, value := range []string{c.Username(), c.Source()} {
		if strings.TrimSpace(value) != value || strings.ContainsAny(value, "/?#") {
			return errors.New("linkd control_plane jwt_username and event_source_id must be canonical names")
		}
	}
	return nil
}

func (c LinkdConfig) Validate() error {
	return c.ControlPlane.validate()
}

// ErrLinkdControlPlaneRequired refuses a Control or Worker of a deployment
// that states the link (a phase_two.linkd block) without a whole control
// plane: it could read none of the link's alerts back or close them - the
// recovery gate would hold every RECOVERY it did not send the ABNORMAL of,
// and no edit, absent, inactive or scope close would be made. The secret is
// part of the plane: the link checks the Internal-Token on every management
// route alarmd calls and has no mode without one, so a plane without it
// would be answered 401 on every call. A deployment without the block is
// never refused, whatever its output protocol.
var ErrLinkdControlPlaneRequired = errors.New("phase_two.linkd is present: its control_plane url and jwt_secret_file are required")

// validateDestination refuses a stated link without a whole control plane.
func (c LinkdConfig) validateDestination() error {
	if !c.Present() || c.ControlPlane.Configured() {
		return nil
	}
	return ErrLinkdControlPlaneRequired
}

type LinkdCapacity struct{ Bytes, Strategies, Members, LocalEntries, ReadBatch, GroupBatch, CloseBatch int }

// linkdCloseBatch is how many closes one step sends. A close is a Kafka
// write, which takes thousands a second; it is not CPU work to ration.
const linkdCloseBatch = 1000

func DeriveLinkdCapacity(in CapacityInputs) LinkdCapacity {
	// The index and calibration metadata receive a sixteenth of container
	// memory, about 512 bytes an alert: 64k alerts and 16k strategies at
	// 512Mi. Local ACK/owner bookkeeping has its own derived limit and the
	// calendar cache its own share. These admission estimates are not an RSS
	// cap, and they are not taken out of the execution share: a share filled
	// to its bound puts the planned heap about a fifteenth over the Go soft
	// limit (40Mi at 512Mi), which is collection pressure, not an OOM, and is
	// reached only at tens of thousands of open alerts. Reserving it would
	// cut a third of the executions for memory that is almost never held.
	bytes := int(min(in.MemoryLimitBytes/16, uint64(^uint(0)>>1)))
	if bytes < 64<<10 {
		return LinkdCapacity{}
	}
	ops := max(1, min(in.CPUBudget*4, bytes/4096))
	return LinkdCapacity{Bytes: bytes, Strategies: bytes / 2048, Members: bytes / 512,
		LocalEntries: bytes / 2048, ReadBatch: ops, GroupBatch: ops, CloseBatch: linkdCloseBatch}
}

// DeriveCalendarCacheCapacity is the legacy calendar (effective time) cache's
// own share: a sixty-fourth of container memory, about 2 KiB an entry.
func DeriveCalendarCacheCapacity(in CapacityInputs) (entries, bytes int) {
	bytes = int(min(in.MemoryLimitBytes/64, uint64(^uint(0)>>1)))
	return bytes / 2048, bytes
}
