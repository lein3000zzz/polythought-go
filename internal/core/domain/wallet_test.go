package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    domain.WalletAddress
		wantErr bool
	}{
		// Vectors from EIP-55.
		{name: "eip-55 vector 1", in: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", want: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{name: "eip-55 vector 2", in: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", want: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"},
		{name: "eip-55 vector 3", in: "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB", want: "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB"},
		{name: "eip-55 vector 4", in: "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb", want: "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"},
		{name: "lowercase gets checksummed", in: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", want: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{name: "uppercase gets checksummed", in: "0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED", want: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{name: "surrounding spaces", in: "  0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed\n", want: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
		{name: "wrong checksum (one letter flipped)", in: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD", wantErr: true},
		{name: "missing 0x", in: "5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", wantErr: true},
		{name: "too short", in: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1bea", wantErr: true},
		{name: "not hex", in: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beazz", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseAddress(tt.in)

			if tt.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidAddress)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSignatureTypeValid(t *testing.T) {
	tests := []struct {
		in   domain.SignatureType
		want bool
	}{
		{domain.SignatureEOA, true},
		{domain.SignaturePolyProxy, true},
		{domain.SignatureGnosisSafe, true},
		{domain.SignatureDepositWallet, true},
		{-1, false},
		{4, false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.in.Valid(), "%d", tt.in)
	}
}
