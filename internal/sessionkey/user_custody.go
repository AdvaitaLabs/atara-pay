package sessionkey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
)

// Prepared is the result of PrepareUserCustody. The caller hands
// UnsignedAuthorize to the wallet owner for off-server signing, then sends
// the signed bytes back via SubmitAuthorize to flip the session key from
// 'pending_authorize' to 'active'.
type Prepared struct {
	ID               string
	PublicAddress    string
	PrivateKey       []byte
	PolicyID         string
	ExpiresAt        time.Time
	NextRotationAt   time.Time
	RotationMode     string
	UnsignedAuthorize *tempo.UnsignedTransfer
}

// PrepareUserCustody mints a session keypair, persists it as
// status='pending_authorize', and returns an unsigned authorizeKey tx for
// the wallet owner to sign. Mirrors Mint() but:
//
//   - never broadcasts (Atara has no master key for user-custody wallets)
//   - leaves the row inactive until SubmitAuthorize lands the signed tx
//   - the returned private bytes are still useful — the agent runtime can
//     start signing speculatively, but on-chain enforcement won't activate
//     until the wallet owner submits the authorizeKey
//
// Pre-conditions enforced here: wallet must be rail=tempo + custody=user,
// service must have a non-nil tempo adapter. Anything else is a 400 to the
// caller.
func (s *Service) PrepareUserCustody(ctx context.Context, in MintInput) (*Prepared, error) {
	if err := validateMint(in); err != nil {
		return nil, err
	}
	if s.tempo == nil {
		return nil, errors.New("sessionkey: user-custody mint requires a tempo adapter")
	}

	wallet, err := s.q.GetWalletByID(ctx, in.WalletID)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: load wallet: %w", err)
	}
	if wallet.Rail != "tempo" {
		return nil, fmt.Errorf("sessionkey: user-custody mint requires rail=tempo (got %q)", wallet.Rail)
	}
	if wallet.Custody != "user" {
		return nil, fmt.Errorf("sessionkey: user-custody mint requires custody=user (got %q)", wallet.Custody)
	}

	// Step 1: generate the session keypair.
	priv, addr, err := tempo.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("sessionkey: generate key: %w", err)
	}
	plain := append([]byte(nil), priv[:]...)
	defer zero(plain)

	encBlob, keyVer, err := s.ks.Encrypt(plain)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: encrypt: %w", err)
	}

	// Step 2: derive timestamps + ids.
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
		rotationIntervalS = 86400
	}
	nextRotation := now.Add(time.Duration(rotationIntervalS) * time.Second)
	if nextRotation.After(expiresAt) {
		nextRotation = expiresAt
	}

	sessionKeyID := id.New(id.PrefixSessionKey)
	policyID := id.New(id.PrefixLimitPolicy)

	// Step 3: build the unsigned authorizeKey tx via the Tempo adapter. No
	// signature happens here — the wallet master key lives off-server.
	var limits []tempo.TokenLimit
	if in.Limits.DailyUSD > 0 {
		limits = append(limits, tempo.PathUSDDailyLimit(in.Limits.DailyUSD*1_000_000))
	}
	unsigned, err := s.tempo.BuildAuthorizeKey(
		ctx,
		wallet.Address,
		addr,
		tempo.SigTypeSecp256k1,
		tempo.KeyRestrictions{
			Expiry:        uint64(expiresAt.Unix()),
			EnforceLimits: true,
			Limits:        limits,
			AllowAnyCalls: false,
			AllowedCalls: []tempo.AllowedCall{
				tempo.TransferOnly(commonPathUSD()),
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: build authorize tx: %w", err)
	}

	// Step 4: persist policy + session key row in one tx, marked
	// pending_authorize. on_chain_tx_hash stays NULL until SubmitAuthorize.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: begin tx: %w", err)
	}
	defer tx.Rollback(context.Background())
	qtx := s.q.WithTx(tx)

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
		Metadata:          []byte(`{"scope":"session_key","custody":"user"}`),
	}); err != nil {
		return nil, fmt.Errorf("sessionkey: insert policy: %w", err)
	}

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
		Status:            "pending_authorize",
		RailNative:        true, // it WILL be — once submit-authorize lands
		OnChainTxHash:     pgxOptText(""),
		Metadata:          []byte(`{"custody":"user"}`),
	}); err != nil {
		return nil, fmt.Errorf("sessionkey: insert row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sessionkey: commit: %w", err)
	}

	return &Prepared{
		ID:                sessionKeyID,
		PublicAddress:     addr,
		PrivateKey:        append([]byte(nil), priv[:]...),
		PolicyID:          policyID,
		ExpiresAt:         expiresAt,
		NextRotationAt:    nextRotation,
		RotationMode:      rotationMode,
		UnsignedAuthorize: unsigned,
	}, nil
}

// SubmitAuthorize broadcasts the wallet-owner-signed authorizeKey tx and
// flips the session key from 'pending_authorize' to 'active', stamping the
// on-chain tx hash. Idempotent w.r.t. broadcast errors — if SendTransaction
// fails the row stays in pending_authorize, so the caller can re-sign with
// a fresher nonce and resubmit.
//
// Returns the on-chain tx hash on success.
func (s *Service) SubmitAuthorize(
	ctx context.Context, tenantID, sessionKeyID, signedTxHex string,
) (string, error) {
	if s.tempo == nil {
		return "", errors.New("sessionkey: SubmitAuthorize requires a tempo adapter")
	}

	row, err := s.q.GetSessionKeyByID(ctx, sessionKeyID)
	if err != nil {
		return "", fmt.Errorf("sessionkey: load row: %w", err)
	}
	if row.TenantID != tenantID {
		// Same 404 either way so cross-tenant probes can't tell whether the
		// id exists under another tenant.
		return "", errors.New("sessionkey: not found")
	}
	if row.Status != "pending_authorize" {
		return "", fmt.Errorf("sessionkey: status is %q; submit-authorize only valid for 'pending_authorize'", row.Status)
	}

	hash, err := s.tempo.BroadcastSignedTx(ctx, signedTxHex)
	if err != nil {
		return "", fmt.Errorf("sessionkey: broadcast authorize: %w", err)
	}

	// Activate the row + stamp the tx hash. Single SQL roundtrip; we use
	// the pool directly because sqlc's UpdateSessionKeyStatus only sets
	// status. When sqlc is regenerated this can move into a proper query.
	if _, err := s.pool.Exec(ctx,
		`UPDATE session_keys SET status = 'active', on_chain_tx_hash = $2 WHERE id = $1`,
		row.ID, hash,
	); err != nil {
		return "", fmt.Errorf("sessionkey: activate row: %w", err)
	}
	return hash, nil
}
