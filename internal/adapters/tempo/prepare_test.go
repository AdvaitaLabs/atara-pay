package tempo

import (
	"bytes"
	"testing"
)

func TestHexDecode(t *testing.T) {
	cases := []struct {
		in      string
		want    []byte
		wantErr bool
	}{
		{"0xdeadbeef", []byte{0xde, 0xad, 0xbe, 0xef}, false},
		{"0XDEADBEEF", []byte{0xde, 0xad, 0xbe, 0xef}, false},
		{"deadbeef", []byte{0xde, 0xad, 0xbe, 0xef}, false},
		{"0xZZ", nil, true},
		{"odd", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := hexDecode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %x", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("got %x, want %x", got, tc.want)
			}
		})
	}
}
