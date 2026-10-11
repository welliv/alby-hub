// Command seed derives the Greenlight node secret (hsm_secret, 32 bytes) from
// the hub's BIP39 mnemonic, exactly as the hub does, and writes it 0600.
// Usage: go run ./scripts/greenlight/seed <mnemonic-file> <out-hsm_secret>
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/getAlby/hub/lnclient/greenlight"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: seed <mnemonic-file> <out-hsm_secret>")
		os.Exit(2)
	}
	m, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	s, err := greenlight.MnemonicToSeed32(strings.TrimSpace(string(m)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid mnemonic")
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], s, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
