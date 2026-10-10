package api

// Adversarial: the backup format is AES-CTR (legacy OFB) with NO MAC. This
// test documents what an attacker WITHOUT the password can do to a backup
// file: XOR-patch any byte whose plaintext they can predict. The zip central
// directory (file names, fixed layout from the known file list) is
// predictable, so entries can be renamed/hidden undetected. Deflated file
// bodies are not predictable, and zip CRC32 catches blind flips there.
// Upstream-wide issue (all backends); recommended fix: AEAD v3 scheme.

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

func TestBackupTamperingRenamesEntryUndetected_KnownIssue(t *testing.T) {
	var plain bytes.Buffer
	zw := zip.NewWriter(&plain)
	for _, n := range []string{"nwc.db", "greenlight/device-creds/ca.pem"} {
		w, _ := zw.Create(n)
		_, _ = w.Write(bytes.Repeat([]byte(n), 50))
	}
	_ = zw.Close()

	var enc bytes.Buffer
	ew, err := encryptingWriter(&enc, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ew.Write(plain.Bytes())

	// attacker: knows the layout, not the password or key
	ct := enc.Bytes()
	hdr := 32 + 16 // salt + iv (current scheme)
	p := plain.Bytes()
	for i := 0; i+6 <= len(p); i++ {
		if bytes.Equal(p[i:i+6], []byte("nwc.db")) && (i == 30 || i > len(p)-200) { // local + central name
			ct[hdr+i+5] ^= 'b' ^ 'x' // "nwc.db" -> "nwc.dx"
		}
	}

	r, err := decryptingReader(bytes.NewReader(ct), "correct horse")
	if err != nil {
		t.Fatalf("tampered file rejected at decrypt (good, but unexpected): %v", err)
	}
	out, _ := io.ReadAll(r)
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip rejected tampering: %v", err)
	}
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if names[0] != "nwc.dx" {
		t.Fatalf("tamper did not take effect: %v", names)
	}
	t.Logf("KNOWN ISSUE: unauthenticated backup accepted with renamed entry: %v (nwc.db would be silently dropped from restore)", names)
}
