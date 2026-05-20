// Package sessionkey mints, looks up, and revokes session keys — delegated
// signers each backed by their own limit_policy.
//
// A session key is exactly the same kind of secp256k1 keypair Tempo wallets
// use, but it lives in session_keys (not wallets), points at its own
// limit_policy, and has a short TTL with optional auto-rotation. AI agents
// carry session keys instead of the master wallet's key, so a leaked key
// is bounded by the daily/per-tx caps on its policy and expires within a
// day.
//
// Tempo native on-chain enforcement (authorizeKey on the AccountKeychain
// precompile) is deliberately NOT wired here. The gateway-side enforcement
// — limits.Service checking on every transfer — works for both rails today.
// On-chain attestation is M5+ once Sponsored Transactions land so the
// keystore tenant can pay the pathUSD gas without exposing the wallet's
// own key. rail_native is therefore set to false on every row at mint
// time.
package sessionkey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/keystore"
)

// Service is concurrency-safe. Build one and share.
type Service struct {
	pool  *pgxpool.Pool
	q     *sqlcgen.Queries
	ks    keystore.Keystore
	tempo *tempo.Adapter // optional — required only when MintInput.OnChainEnforce is set
}

// New constructs a Service. Keystore is required — every session key's
// private bytes go through it before persistence.
//
// tempoAdapter is optional: pass nil to disable on-chain enforcement. When
// nil, MintInput.OnChainEnforce=true is rejected at validateMint time so
// callers get a clean error instead of a half-completed mint.
func New(pool *pgxpool.Pool, ks keystore.Keystore, tempoAdapter *tempo.Adapter) *Service {
	return &Service{
		pool:  pool,
		q:     sqlcgen.New(pool),
		ks:    ks,
		tempo: tempoAdapter,
	}
}

// Limits describes the spending caps stamped onto the session key's
// dedicated limit_policy row. Every cap is integer USD; the service
// promotes them to pgtype.Numeric on INSERT.
type Limits struct {
	PerTxUSD   int64
	DailyUSD   int64
	WeeklyUSD  int64
	MonthlyUSD int64

	// Optional whitelist / blacklist. Empty arrays mean "no list".
	AllowedRecipients []string
	DeniedRecipients  []string
}

// MintInput is everything Mint() needs to provision a session key.
type MintInput struct {
	TenantID string
	WalletID string
	GroupID  string

	Name string

	Limits Limits

	// RotationMode is one of auto_rotate | notify_only | hard_expire.
	// Empty input defaults to auto_rotate.
	RotationMode      string
	RotationIntervalS int32
	// ExpiresAt zero → default 24h from now.
	ExpiresAt time.Time

	// OnChainEnforce, when true, broadcasts an AccountKeychain.authorizeKey
	// call to Tempo using the wallet's master key. The resulting
	// session_keys row carries rail_native=true + on_chain_tx_hash. Only
	// valid when the wallet is rail=tempo / custody=platform; mismatches
	// fail before any signature happens. nil tempo Adapter on the Service
	// also fails fast.
	OnChainEnforce bool
}

// Minted is what Mint returns to the caller. PrivateKey is the ONLY way the
// raw secret will ever leave the server — store it immediately.
type Minted struct {
	ID             string
	PublicAddress  string
	PrivateKey     []byte // 32-byte secp256k1 raw key
	PolicyID       string
	ExpiresAt      time.Time
	NextRotationAt time.Time
	RotationMode   string

	// On-chain attestation. Populated only when MintInput.OnChainEnforce
	// was true and the authorizeKey broadcast succeeded.
	RailNative    bool
	OnChainTxHash string
}

// Mint provisions a new session key:
//
//	1. Generate a secp256k1 keypair (tempo.GenerateKeypair — same primitive
//	   as a Tempo wallet, since the rail's signature format is the same).
//	2. Encrypt the private bytes with the keystore.
//	3. In ONE transaction:
//	    a. INSERT a dedicated limit_policy (scope_type='session_key',
//	       scope_id = the future session_key id).
//	    b. INSERT the session_keys row pointing at that policy.
//	4. Return the raw private key + structured Minted view.
//
// The caller then hands PrivateKey to the AI agent's runtime. Atara never
// stores it in plaintext again.
func (s *Service) Mint(ctx context.Context, in MintInput) (*Minted, error) {
	if err := validateMint(in); err != nil {
		return nil, err
	}

	// Step 1: generate keypair (CPU-only).
	priv, addr, err := tempo.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("sessionkey: generate key: %w", err)
	}

	// Step 2: encrypt with the keystore. We copy priv[:] before passing in
	// so the keystore implementation can't pin our stack-array.
	plain := append([]byte(nil), priv[:]...)
	defer zero(plain)

	encBlob, keyVer, err := s.ks.Encrypt(plain)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: encrypt: %w", err)
	}

	// Step 3: derive timestamps + ids.
	now := time.Now().UTC()
	expiresAt := in.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(24 * time.Hour)
	}
	rotationMode := in.RotationMode
	if rotationMode == "" {
		rotationMode = "auto_rotate"
	}
	rotationIntervalS := in.RotationIntervalS
	if rotationIntervalS <= 0 {
		rotationIntervalS = 86400 // 24h
	}
	nextRotation := now.Add(time.Duration(rotationIntervalS) * time.Second)
	// Don't schedule a rotation after the row has expired.
	if nextRotation.After(expiresAt) {
		nextRotation = expiresAt
	}

	sessionKeyID := id.New(id.PrefixSessionKey)
	policyID := id.New(id.PrefixLimitPolicy)

	// Step 3b (optional): on-chain authorizeKey.
	//
	// Done OUTSIDE the DB transaction so a broadcast failure leaves no DB
	// state behind to clean up (we just return the error and let the
	// caller retry). On success we capture the tx hash and stamp it into
	// the session_keys row in step 4b.
	var (
		railNative    bool
		onChainTxHash string
	)
	if in.OnChainEnforce {
		hash, err := s.runOnChainAuthorize(ctx, in, addr, expiresAt)
		if err != nil {
			return nil, err
		}
		railNative = true
		onChainTxHash = hash
	}

	// Step 4: short atomic transaction.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: begin tx: %w", err)
	}
	defer tx.Rollback(context.Background())
	qtx := s.q.WithTx(tx)

	// 4a: limit_policy. scope_type='session_key', scope_id = session key id.
	// (limit_policies.scope_id is a plain TEXT column, not an FK — we can
	// reference the future session_keys.id safely.)
	if _, err := qtx.CreateLimitPolicy(ctx, sqlcgen.CreateLimitPolicyParams{
		ID:                policyID,
		TenantID:          in.TenantID,
		ScopeType:         "session_key",
		ScopeID:           pgxOptText(sessionKeyID),
		PerTxAmount:       pgxIntUSD(in.Limits.PerTxUSD),
		PerTxAsset:        pgxOptText("USDC"),
		DailyAmount:       pgxIntUSD(in.Limits.DailyUSD),
		WeeklyAmount:      pgxIntUSD(in.Limits.WeeklyUSD),
		MonthlyAmount:     pgxIntUSD(in.Limits.MonthlyUSD),
		PeriodAsset:       "USDC",
		Timezone:          "UTC",
		ResetDayOfWeek:    1,
		ResetDayOfMonth:   1,
		AllowedRecipients: marshalStringList(in.Limits.AllowedRecipients),
		DeniedRecipients:  marshalStringList(in.Limits.DeniedRecipients),
		Enabled:           true,
		Metadata:          []byte(`{"scope":"session_key"}`),
	}); err != nil {
		return nil, fmt.Errorf("sessionkey: insert policy: %w", err)
	}

	// 4b: session_keys row referencing the policy we just wrote.
	if _, err := qtx.CreateSessionKey(ctx, sqlcgen.CreateSessionKeyParams{
		ID:                sessionKeyID,
		TenantID:          in.TenantID,
		WalletID:          in.WalletID,
		GroupID:           in.GroupID,
		Name:              in.Name,
		PublicAddress:     addr,
		EncryptedPrivKey:  encBlob,
		KeyVersion:        keyVer,
		PolicyID:          pgxOptText(policyID),
		RotationMode:      rotationMode,
		RotationIntervalS: rotationIntervalS,
		NextRotationAt:    pgxOptTimestamp(nextRotation),
		ExpiresAt:         pgxRequiredTimestamp(expiresAt),
		Status:            "active",
		RailNative:        railNative,
		OnChainTxHash:     pgxOptText(onChainTxHash),
		Metadata:          []byte("{}"),
	}); err != nil {
		return nil, fmt.Errorf("sessionkey: insert row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sessionkey: commit: %w", err)
	}

	// Hand the plaintext back. Caller stores it once and never asks again.
	return &Minted{
		ID:             sessionKeyID,
		PublicAddress:  addr,
		PrivateKey:     append([]byte(nil), priv[:]...),
		PolicyID:       policyID,
		ExpiresAt:      expiresAt,
		NextRotationAt: nextRotation,
		RotationMode:   rotationMode,
		RailNative:     railNative,
		OnChainTxHash:  onChainTxHash,
	}, nil
}

// runOnChainAuthorize loads the wallet, decrypts its master key, builds
// the KeyRestrictions snapshot, broadcasts the AccountKeychain
// .authorizeKey() call, and returns the resulting tx hash.
//
// Sequence is deliberately tight around the plaintext master key:
// decrypt → use → zero. The plaintext NEVER reaches a goroutine boundary,
// so escape analysis keeps it on the stack of this function alone.
func (s *Service) runOnChainAuthorize(
	ctx context.Context, in MintInput, sessionKeyAddr string, expiresAt time.Time,
) (string, error) {
	if s.tempo == nil {
		return "", errors.New("sessionkey: on-chain enforce requested but tempo adapter not configured")
	}

	wallet, err := s.q.GetWalletByID(ctx, in.WalletID)
	if err != nil {
		return "", fmt.Errorf("sessionkey: load wallet: %w", err)
	}
	if wallet.Rail != "tempo" {
		return "", fmt.Errorf("sessionkey: on-chain enforce requires rail=tempo (got %q)", wallet.Rail)
	}
	if wallet.Custody != "platform" {
		return "", fmt.Errorf("sessionkey: on-chain enforce requires custody=platform (got %q)", wallet.Custody)
	}
	if len(wallet.EncryptedPrivateKey) == 0 || !wallet.KeyVersion.Valid {
		return "", errors.New("sessionkey: wallet has no encrypted master key (schema invariant violated)")
	}

	master, err := s.ks.Decrypt(wallet.EncryptedPrivateKey, wallet.KeyVersion.Int16)
	if err != nil {
		return "", fmt.Errorf("sessionkey: decrypt master: %w", err)
	}
	defer zero(master)

	// Translate the policy's daily cap into a single on-chain TokenLimit
	// for pathUSD. Weekly / monthly stay gateway-only — the precompile's
	// (token, period) model supports them too, but the dashboard story
	// for "your AI agent has 3 simultaneous chain-enforced caps" lands
	// in M9. Per-tx is gateway-enforced regardless; the chain has no
	// per-call concept.
	var limits []tempo.TokenLimit
	if in.Limits.DailyUSD > 0 {
		limits = append(limits, tempo.PathUSDDailyLimit(in.Limits.DailyUSD*1_000_000))
	}

	txHash, err := s.tempo.AuthorizeKey(ctx, tempo.AuthorizeKeyInput{
		WalletPrivKey:     master,
		WalletAddress:     wallet.Address,
		SessionKeyAddress: sessionKeyAddr,
		SignatureType:     tempo.SigTypeSecp256k1,
		Restrictions: tempo.KeyRestrictions{
			Expiry:        uint64(expiresAt.Unix()),
			EnforceLimits: true,
			Limits:        limits,
			AllowAnyCalls: false,
			AllowedCalls: []tempo.AllowedCall{
				tempo.TransferOnly(commonPathUSD()),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("sessionkey: on-chain authorize: %w", err)
	}
	return txHash, nil
}

// validateMint catches the inputs we never want to persist.
func validateMint(in MintInput) error {
	if in.TenantID == "" || in.WalletID == "" || in.GroupID == "" {
		return errors.New("sessionkey: TenantID / WalletID / GroupID are required")
	}
	if in.Name == "" {
		return errors.New("sessionkey: Name is required")
	}
	switch in.RotationMode {
	case "", "auto_rotate", "notify_only", "hard_expire":
	default:
		return fmt.Errorf("sessionkey: invalid rotation_mode %q", in.RotationMode)
	}
	if in.Limits.PerTxUSD < 0 || in.Limits.DailyUSD < 0 ||
		in.Limits.WeeklyUSD < 0 || in.Limits.MonthlyUSD < 0 {
		return errors.New("sessionkey: caps must be non-negative")
	}
	if in.ExpiresAt.IsZero() {
		return nil
	}
	if time.Now().UTC().After(in.ExpiresAt) {
		return errors.New("sessionkey: ExpiresAt is in the past")
	}
	return nil
}

// zero wipes b. Defensive; Go's stack escape analysis may keep plaintext
// around longer than strictly needed.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
