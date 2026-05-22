package handlers

import (
	"strings"
	"testing"
)

func TestValidatePrepareTx(t *testing.T) {
	cases := []struct {
		name       string
		req        PrepareTransactionRequest
		wantOK     bool
		wantSubstr string
	}{
		{
			name:   "full request ok",
			req:    PrepareTransactionRequest{To: "0xabc", Amount: "1.5", Asset: "USDC"},
			wantOK: true,
		},
		{
			name:       "missing to",
			req:        PrepareTransactionRequest{Amount: "1.5", Asset: "USDC"},
			wantSubstr: "to is required",
		},
		{
			name:       "missing amount",
			req:        PrepareTransactionRequest{To: "0xabc", Asset: "USDC"},
			wantSubstr: "amount is required",
		},
		{
			name:       "missing asset",
			req:        PrepareTransactionRequest{To: "0xabc", Amount: "1.5"},
			wantSubstr: "asset is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePrepareTx(tc.req)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("err=%v, want substring %q", err, tc.wantSubstr)
			}
		})
	}
}
