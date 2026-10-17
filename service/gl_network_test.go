package service

import "testing"

func TestGreenlightNetwork(t *testing.T) {
	ok := map[string]string{
		"bitcoin": "bitcoin", "mainnet": "bitcoin",
		"testnet": "testnet", "testnet3": "testnet",
		"signet": "signet", "regtest": "regtest", "": "signet",
	}
	for in, want := range ok {
		got, err := greenlightNetwork(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"mainet", "Bitcoin", "testnet4", "liquid"} {
		if got, err := greenlightNetwork(bad); err == nil {
			t.Errorf("%q: want error, got %q", bad, got)
		}
	}
}
