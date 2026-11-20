package aliceplugin

import "testing"

func TestPolicy_EmptyAllowlistDeniesEveryone(t *testing.T) {
	p := NewPolicy(nil, false)
	if got := p.Check("anybody"); got != Deny {
		t.Fatalf("empty allowlist must deny, got %v", got)
	}
	if p.IsAllowed("anybody") {
		t.Fatal("IsAllowed must be false for empty allowlist")
	}
}

func TestPolicy_EmptyAllowlistWithDebugRevealsID(t *testing.T) {
	p := NewPolicy(nil, true)
	if got := p.Check("anybody"); got != Debug {
		t.Fatalf("empty allowlist + debug must return Debug, got %v", got)
	}
	if p.IsAllowed("anybody") {
		t.Fatal("Debug must not be treated as allowed")
	}
}

func TestPolicy_AllowlistedUserPasses(t *testing.T) {
	p := NewPolicy([]string{"user-1", "user-2"}, false)
	for _, id := range []string{"user-1", "user-2"} {
		if got := p.Check(id); got != Allow {
			t.Fatalf("%s should be allowed, got %v", id, got)
		}
	}
}

func TestPolicy_UnknownUserDenied(t *testing.T) {
	p := NewPolicy([]string{"user-1"}, false)
	if got := p.Check("intruder"); got != Deny {
		t.Fatalf("unknown user must be denied, got %v", got)
	}
}

func TestPolicy_UnknownUserInDebugMode(t *testing.T) {
	p := NewPolicy([]string{"user-1"}, true)
	if got := p.Check("intruder"); got != Debug {
		t.Fatalf("unknown user + debug must be Debug, got %v", got)
	}
	// Allowlisted user still passes even in debug mode.
	if got := p.Check("user-1"); got != Allow {
		t.Fatalf("allowlisted user must be allowed in debug mode, got %v", got)
	}
}

func TestPolicy_EmptyUserIDAlwaysDenied(t *testing.T) {
	for _, debug := range []bool{false, true} {
		p := NewPolicy([]string{"user-1"}, debug)
		if got := p.Check(""); got != Deny {
			t.Fatalf("empty user_id must be denied (debug=%v), got %v", debug, got)
		}
		if got := p.Check("   "); got != Deny {
			t.Fatalf("whitespace user_id must be denied (debug=%v), got %v", debug, got)
		}
	}
}

func TestPolicy_TrimsAndDropsEmptyEntries(t *testing.T) {
	p := NewPolicy([]string{"  user-1  ", "", "   "}, false)
	if len(p.AllowedUserIDs) != 1 {
		t.Fatalf("expected 1 cleaned entry, got %d: %#v", len(p.AllowedUserIDs), p.AllowedUserIDs)
	}
	if got := p.Check("user-1"); got != Allow {
		t.Fatalf("trimmed entry must match, got %v", got)
	}
	// A stray empty string in config must not act as a wildcard.
	if got := p.Check(""); got != Deny {
		t.Fatalf("empty entry must not allow empty user, got %v", got)
	}
}

func TestPolicy_ExactMatchOnly(t *testing.T) {
	p := NewPolicy([]string{"user-1"}, false)
	if got := p.Check("user-10"); got != Deny {
		t.Fatalf("prefix must not match, got %v", got)
	}
	if got := p.Check("USER-1"); got != Deny {
		t.Fatalf("case must be significant, got %v", got)
	}
}
