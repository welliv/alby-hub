package greenlight

import (
	"regexp"
	"strings"

	"github.com/tyler-smith/go-bip39"
)

// any line mentioning a mnemonic/seed phrase/secret is dropped entirely
var reSecretLine = regexp.MustCompile(`(?im)^.*(mnemonic|seed phrase|hsm_secret contents|secret key|private key).*$`)

// sanitizeGlcliOutput removes secrets from glcli output before it is logged
// or returned in an error: lines mentioning a mnemonic, and any run of 12+
// consecutive BIP39 words anywhere in the text.
func sanitizeGlcliOutput(s string) string {
	if len(s) > 1<<20 {
		s = s[:1<<20]
	}
	s = reSecretLine.ReplaceAllString(s, "[redacted]")
	lines := strings.Split(s, "\n")
	words := bip39.GetWordList()
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	for i, line := range lines {
		fields := strings.Fields(line)
		run := 0
		for _, f := range fields {
			if _, ok := set[strings.ToLower(strings.Trim(f, ".,:;\"'"))]; ok {
				run++
				if run >= 12 {
					lines[i] = "[redacted]"
					break
				}
			} else {
				run = 0
			}
		}
	}
	return strings.Join(lines, "\n")
}

// SanitizeOutput is the exported form for the hub's signer supervisor.
func SanitizeOutput(s string) string { return sanitizeGlcliOutput(s) }
