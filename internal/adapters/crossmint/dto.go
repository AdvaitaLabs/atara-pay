package crossmint

// DTOs mirror the CrossMint REST shapes. Keep them dumb — translation lives in adapter.go.

// --- Wallet ---

type walletRequest struct {
	Type   string         `json:"type"`             // "smart" | "evm-mpc" | etc.
	Chain  string         `json:"chain,omitempty"`  // "base", "polygon", "solana", ...
	Linked walletLinked   `json:"linkedUser,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

type walletLinked struct {
	Email    string `json:"email,omitempty"`
	UserID   string `json:"userId,omitempty"`
	Phone    string `json:"phoneNumber,omitempty"`
	External string `json:"externalUserId,omitempty"`
}

type walletResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Chain      string `json:"chain"`
	Address    string `json:"address"`
	LinkedUser any    `json:"linkedUser,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

// --- Balances ---

type balanceItem struct {
	Token    string `json:"token"`
	Chain    string `json:"chain"`
	Amount   string `json:"amount"`
	Decimals int    `json:"decimals"`
}

type balancesResponse []balanceItem

// --- Transactions ---

type txRequest struct {
	Params txParams `json:"params"`
}

type txParams struct {
	Chain     string  `json:"chain,omitempty"`
	Calls     []txCall `json:"calls,omitempty"`
	// For simple token transfers (calls is empty), CrossMint also accepts a
	// dedicated /tokens/{locator}/transfers endpoint — we use that path in
	// adapter.go for clarity.
}

type txCall struct {
	To    string `json:"to"`
	Value string `json:"value,omitempty"`
	Data  string `json:"data,omitempty"`
}

type txResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`     // "pending" | "success" | "failed" | ...
	OnChain    *txOnChain `json:"onChain,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

type txOnChain struct {
	Chain  string `json:"chain"`
	TxHash string `json:"txHash"`
}

// --- Token transfer (typed endpoint) ---

type tokenTransferRequest struct {
	Recipient string `json:"recipient"` // address or "email:foo@bar"
	Amount    string `json:"amount"`
}

// --- Onramp / hosted checkout ---

type onrampOrderRequest struct {
	Recipient onrampRecipient `json:"recipient"`
	Payment   onrampPayment   `json:"payment"`
	LineItems []onrampItem    `json:"lineItems"`
}

type onrampRecipient struct {
	WalletAddress string `json:"walletAddress"`
}

type onrampPayment struct {
	Method   string `json:"method"`   // "fiat"
	Currency string `json:"currency"` // "usd"
	PayerEmail string `json:"payerEmail,omitempty"`
}

type onrampItem struct {
	TokenLocator string `json:"tokenLocator"` // e.g. "base:usdc"
	ExecutionParameters onrampExec `json:"executionParameters"`
}

type onrampExec struct {
	Mode   string `json:"mode"` // "exact-in"
	Amount string `json:"amount"`
}

type onrampOrderResponse struct {
	Order struct {
		OrderID string `json:"orderId"`
		Phase   string `json:"phase"`
	} `json:"order"`
	OrderClientSecret string `json:"orderClientSecret,omitempty"`
	HostedCheckoutURL string `json:"hostedCheckoutUrl,omitempty"`
}
