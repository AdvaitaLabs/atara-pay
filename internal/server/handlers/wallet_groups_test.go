package handlers

import (
	"strings"
	"testing"
)

// TestValidateCreateGroup_Custody covers the user/platform custody branches
// added in M13.4 (self-custody Phase 1). The validator is pure — no DB / no
// rail calls — so this is a fast table-driven test.
func TestValidateCreateGroup_Custody(t *testing.T) {
	type ext = map[string]string
	mk := func(custody string, addrs ext) CreateGroupRequest {
		var r CreateGroupRequest
		r.Owner.Type = "user"
		r.Owner.Ref = "alice-uid-123"
		r.Custody = custody
		r.ExternalAddresses = addrs
		return r
	}
	cases := []struct {
		name      string
		req       CreateGroupRequest
		wantOK    bool
		wantSubstr string
	}{
		{
			name:   "default custody is platform-equivalent",
			req:    mk("", nil),
			wantOK: true,
		},
		{
			name:   "explicit platform with no external addresses",
			req:    mk("platform", nil),
			wantOK: true,
		},
		{
			name:       "platform must not carry external_addresses",
			req:        mk("platform", ext{"tempo": "0xabc"}),
			wantSubstr: `external_addresses only valid when custody="user"`,
		},
		{
			name:   "user custody with one tempo address",
			req:    mk("user", ext{"tempo": "0xabcdef"}),
			wantOK: true,
		},
		{
			name:   "user custody with both addresses",
			req:    mk("user", ext{"tempo": "0xabc", "crossmint": "0xdef"}),
			wantOK: true,
		},
		{
			name:       "user custody requires at least one address",
			req:        mk("user", nil),
			wantSubstr: `external_addresses is required`,
		},
		{
			name:       "user custody rejects unknown rail",
			req:        mk("user", ext{"loka-ln": "ln1abc"}),
			wantSubstr: `unsupported rail`,
		},
		{
			name:       "user custody rejects empty address",
			req:        mk("user", ext{"tempo": ""}),
			wantSubstr: `address is empty`,
		},
		{
			name: "user custody rejects too-long address",
			req: mk("user", ext{
				"tempo": strings.Repeat("a", 201),
			}),
			wantSubstr: `address too long`,
		},
		{
			name:       "unknown custody value rejected",
			req:        mk("hybrid", nil),
			wantSubstr: `custody must be one of`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCreateGroup(tc.req)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}
