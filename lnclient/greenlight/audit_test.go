package greenlight

import (
	"strings"
	"testing"
)

func TestAuditMnemonicRedaction(t *testing.T) {
	in := "New seed generated from mnemonic: abandon abandon ability\n"
	out := reMnemonic.ReplaceAllString(in, "mnemonic: [redacted]")
	if strings.Contains(out, "abandon") {
		t.Fatalf("mnemonic NOT redacted: %q", out)
	}
}

func FuzzIncomingPaymentDecode(f *testing.F) {
	f.Add([]byte{0x0a, 0x02, 0x01, 0x02})
	f.Fuzz(func(t *testing.T, b []byte) {
		m := &incomingPaymentMessage{}
		_ = m.decode(b)
	})
}

func FuzzHostOf(f *testing.F) {
	f.Add("gl1abc.gl.blckstrm.com:443")
	f.Fuzz(func(t *testing.T, s string) { _ = hostOf(normalizeNodeURI(s)) })
}
