package aliceplugin

import (
	"strings"
)

// Decision is the outcome of an access check.
type Decision int

const (
	// Deny means the caller is not allowed to use the skill.
	Deny Decision = iota
	// Allow means the caller is allowed and the command may be processed.
	Allow
	// Debug means the caller is not allowed, but debug mode is on, so we reply
	// with their user_id instead of a flat refusal (lets the owner enroll).
	Debug
)

// String makes test failures readable.
func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Debug:
		return "debug"
	default:
		return "deny"
	}
}

// Policy is the access-control rule set. It is a pure value type with no I/O so
// it can be unit-tested directly.
//
// Fail-closed semantics:
//
//   - empty allowlist  -> nobody is allowed
//   - empty caller ID  -> denied (cannot be verified)
//   - debug mode only affects *unknown* users; allowlisted users always pass
type Policy struct {
	// AllowedUserIDs is the exact-match allowlist of Yandex user IDs.
	AllowedUserIDs []string
	// DebugMode, when true, answers unknown users with their own user_id.
	DebugMode bool
}

// NewPolicy builds a Policy from settings, trimming whitespace and dropping
// empty entries so a stray "" in the config cannot open the door.
func NewPolicy(allowed []string, debug bool) Policy {
	clean := make([]string, 0, len(allowed))
	for _, id := range allowed {
		if id = strings.TrimSpace(id); id != "" {
			clean = append(clean, id)
		}
	}
	return Policy{AllowedUserIDs: clean, DebugMode: debug}
}

// IsAllowed reports whether the given Yandex user_id may use the skill.
func (p Policy) IsAllowed(userID string) bool {
	return p.Check(userID) == Allow
}

// Check returns the full decision for a caller.
func (p Policy) Check(userID string) Decision {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		// No identity -> nothing to match against -> refuse.
		return Deny
	}
	for _, id := range p.AllowedUserIDs {
		if id == userID {
			return Allow
		}
	}
	// Fail-closed: empty allowlist denies everyone.
	if p.DebugMode {
		return Debug
	}
	return Deny
}
