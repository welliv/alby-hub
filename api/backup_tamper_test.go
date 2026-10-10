package api

// With AES-GCM (v3) encryptingWriter, ciphertext tampering must be rejected.
// Pre-AEAD CTR/OFB backups remain importable but are unauthenticated — see
// TestBackupStillImportsLegacyCTR and the evidence pack.

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"io"
	"testing"
)

func mustAES(key []byte) cipher.Block {
	b, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	return b
}

func TestBackupTamperingRejectedWithAEAD(t *testing.T) {
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
	if _, err := ew.Write(plain.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := ew.Close(); err != nil {
		t.Fatal(err)
	}

	ct := append([]byte(nil), enc.Bytes()...)
	// Flip a byte in the ciphertext body (past salt+nonce).
	if len(ct) < 64 {
		t.Fatal("ciphertext too short")
	}
	ct[len(ct)/2] ^= 0x5a

	if _, err := decryptingReader(bytes.NewReader(ct), "correct horse"); err == nil {
		// If decrypt somehow succeeded, ensure zip contents weren't silently renamed.
		t.Fatal("AEAD must reject tampered backup")
	}
}

func TestBackupTamperingRenamesEntryUndetected_KnownIssue(t *testing.T) {
	// Documents the pre-AEAD CTR malleability using backupCiphers[1] directly.
	scheme := backupCiphers[1]
	var plain bytes.Buffer
	zw := zip.NewWriter(&plain)
	for _, n := range []string{"nwc.db", "ca.pem"} {
		w, _ := zw.Create(n)
		_, _ = w.Write(bytes.Repeat([]byte(n), 50))
	}
	_ = zw.Close()

	salt := bytes.Repeat([]byte{9}, scheme.saltSize)
	iv := bytes.Repeat([]byte{8}, 16)
	key, _ := scheme.deriveKey("correct horse", salt)
	block := mustAES(key)
	var enc bytes.Buffer
	_, _ = enc.Write(salt)
	_, _ = enc.Write(iv)
	sw := &cipher.StreamWriter{S: scheme.newStream(block, iv), W: &enc}
	_, _ = sw.Write(plain.Bytes())

	ct := enc.Bytes()
	hdr := scheme.saltSize + 16
	p := plain.Bytes()
	for i := 0; i+6 <= len(p); i++ {
		if bytes.Equal(p[i:i+6], []byte("nwc.db")) && (i == 30 || i > len(p)-200) {
			ct[hdr+i+5] ^= 'b' ^ 'x'
		}
	}
	r, err := decryptingReader(bytes.NewReader(ct), "correct horse")
	if err != nil {
		t.Fatalf("CTR legacy should still decrypt: %v", err)
	}
	out, _ := io.ReadAll(r)
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	if zr.File[0].Name != "nwc.dx" {
		t.Fatalf("expected documented CTR malleability, got %q", zr.File[0].Name)
	}
	t.Log("KNOWN ISSUE (legacy CTR only): entry rename undetected; new backups use AEAD")
}
