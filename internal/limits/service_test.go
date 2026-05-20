package limits

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// mkNumeric is the deterministic way to set pgtype.Numeric in tests.
// Bypasses MarshalJSON/UnmarshalJSON which behaves differently for
// JSON-string vs JSON-number inputs.
func mkNumeric(intVal int64, exp int32) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(intVal), Exp: exp, Valid: true}
}

// fakeQ is the in-memory Querier the tests use. Each method maps to a
// preloaded result; missing entries return ErrNoRows.
type fakeQ struct {
	policies        map[string]sqlcgen.LimitPolicy  // id → policy
	policyByScope   map[string]sqlcgen.LimitPolicy  // "scope:type:id" → policy
	tenantDefault   map[string]sqlcgen.LimitPolicy  // tenant_id → policy
	wallets         map[string]sqlcgen.Wallet       // wallet_id → wallet
	violationsSaved int
}

var errNotFound = errors.New("not found")

func (f *fakeQ) GetTenantDefaultPolicy(_ context.Context, t string) (sqlcgen.LimitPolicy, error) {
	if p, ok := f.tenantDefault[t]; ok {
		return p, nil
	}
	return sqlcgen.LimitPolicy{}, errNotFound
}

func (f *fakeQ) GetPolicyForScope(_ context.Context, arg sqlcgen.GetPolicyForScopeParams) (sqlcgen.LimitPolicy, error) {
	id := ""
	if arg.ScopeID.Valid {
		id = arg.ScopeID.String
	}
	key := arg.ScopeType + ":" + id
	if p, ok := f.policyByScope[key]; ok {
		return p, nil
	}
	return sqlcgen.LimitPolicy{}, errNotFound
}

func (f *fakeQ) GetWalletByID(_ context.Context, id string) (sqlcgen.Wallet, error) {
	if w, ok := f.wallets[id]; ok {
		return w, nil
	}
	return sqlcgen.Wallet{}, errNotFound
}

func (f *fakeQ) CreateLimitViolation(_ context.Context, _ sqlcgen.CreateLimitViolationParams) (sqlcgen.LimitViolation, error) {
	f.violationsSaved++
	return sqlcgen.LimitViolation{}, nil
}

// helper to mint a baseline policy.
func mkPolicy(id, tenantID string) sqlcgen.LimitPolicy {
	return sqlcgen.LimitPolicy{
		ID: id, TenantID: tenantID,
		ScopeType:         "tenant_default",
		Enabled:           true,
		AllowedRecipients: []byte("[]"),
		DeniedRecipients:  []byte("[]"),
	}
}

// ──────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────

func TestNoPolicyAllowsEverything(t *testing.T) {
	svc := New(&fakeQ{}, nil)
	res, err := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "1000000", Asset: "USDC",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Errorf("no policy must allow; got %+v", res)
	}
}

func TestDisabledPolicyTreatedAsAbsent(t *testing.T) {
	p := mkPolicy("pol_1", "tn_x")
	p.Enabled = false
	svc := New(&fakeQ{tenantDefault: map[string]sqlcgen.LimitPolicy{"tn_x": p}}, nil)
	res, _ := svc.Check(context.Background(), CheckRequest{TenantID: "tn_x", Amount: "10"})
	if !res.Allowed {
		t.Errorf("disabled policy must not block")
	}
}

func TestExpiredPolicyBlocks(t *testing.T) {
	p := mkPolicy("pol_1", "tn_x")
	p.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	q := &fakeQ{tenantDefault: map[string]sqlcgen.LimitPolicy{"tn_x": p}}
	svc := New(q, nil)
	res, _ := svc.Check(context.Background(), CheckRequest{TenantID: "tn_x", Amount: "1"})
	if res.Allowed {
		t.Errorf("expired policy must block")
	}
	if res.Violation.Type != "policy_expired" {
		t.Errorf("violation type = %q, want policy_expired", res.Violation.Type)
	}
	if q.violationsSaved != 1 {
		t.Errorf("violation should be persisted; saved=%d", q.violationsSaved)
	}
}

func TestPerTxCap(t *testing.T) {
	p := mkPolicy("pol_1", "tn_x")
	p.PerTxAmount = mkNumeric(5, 0) // exactly 5

	q := &fakeQ{tenantDefault: map[string]sqlcgen.LimitPolicy{"tn_x": p}}
	svc := New(q, nil)

	// Under cap: allowed.
	if res, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "4.5",
	}); !res.Allowed {
		t.Errorf("4.5 should pass cap 5; got %+v", res)
	}

	// Over cap: blocked.
	res, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "5.01",
	})
	if res.Allowed {
		t.Errorf("5.01 should fail cap 5")
	}
	if res.Violation.Type != "per_tx_exceeded" {
		t.Errorf("violation type = %q", res.Violation.Type)
	}
}

func TestAllowlistMode(t *testing.T) {
	p := mkPolicy("pol_1", "tn_x")
	p.AllowedRecipients = []byte(`["merchant:openai","merchant:prakasa"]`)

	q := &fakeQ{tenantDefault: map[string]sqlcgen.LimitPolicy{"tn_x": p}}
	svc := New(q, nil)

	if r, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "1", Recipient: "merchant:openai",
	}); !r.Allowed {
		t.Errorf("whitelisted recipient must pass")
	}
	if r, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "1", Recipient: "merchant:UNAPPROVED",
	}); r.Allowed || r.Violation.Type != "recipient_not_allowlisted" {
		t.Errorf("non-whitelisted recipient must fail with recipient_not_allowlisted; got %+v", r)
	}
}

func TestDenylistMode(t *testing.T) {
	p := mkPolicy("pol_1", "tn_x")
	p.AllowedRecipients = []byte("[]")
	p.DeniedRecipients = []byte(`["0xBAD"]`)

	q := &fakeQ{tenantDefault: map[string]sqlcgen.LimitPolicy{"tn_x": p}}
	svc := New(q, nil)

	if r, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "1", Recipient: "0xGOOD",
	}); !r.Allowed {
		t.Errorf("unlisted recipient must pass in denylist mode")
	}
	if r, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "1", Recipient: "0xBAD",
	}); r.Allowed || r.Violation.Type != "recipient_denied" {
		t.Errorf("denied recipient must fail; got %+v", r)
	}
}

func TestPolicyResolutionPrefersMostSpecific(t *testing.T) {
	tenantPol := mkPolicy("pol_tenant", "tn_x")
	walletPol := mkPolicy("pol_wallet", "tn_x")
	walletPol.ScopeType = "wallet"
	walletPol.PerTxAmount = mkNumeric(1, 0) // exactly 1

	q := &fakeQ{
		tenantDefault: map[string]sqlcgen.LimitPolicy{"tn_x": tenantPol},
		policyByScope: map[string]sqlcgen.LimitPolicy{"wallet:wlt_a": walletPol},
	}
	svc := New(q, nil)

	// Without WalletID set, only tenant_default applies — no cap, big amount OK.
	if r, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", Amount: "100",
	}); !r.Allowed {
		t.Errorf("tenant policy with no cap must allow 100")
	}

	// With WalletID=wlt_a, wallet-specific policy kicks in with cap 1.
	r, _ := svc.Check(context.Background(), CheckRequest{
		TenantID: "tn_x", WalletID: "wlt_a", Amount: "2",
	})
	if r.Allowed {
		t.Errorf("wallet policy cap=1 must block amount=2")
	}
	if r.PolicyID != "pol_wallet" {
		t.Errorf("policy_id = %q, want pol_wallet (most-specific wins)", r.PolicyID)
	}
}
