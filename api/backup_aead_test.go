package api

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"io"
	"testing"
)

func TestBackupAEADRoundTrip(t *testing.T) {
	var plain bytes.Buffer
	zw := zip.NewWriter(&plain)
	w, _ := zw.Create("nwc.db")
	_, _ = w.Write([]byte("sqlite-bytes"))
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

	r, err := decryptingReader(bytes.NewReader(enc.Bytes()), "correct horse")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	out, _ := io.ReadAll(r)
	if !bytes.Equal(out, plain.Bytes()) {
		t.Fatal("round-trip mismatch")
	}
}

func TestBackupAEADRejectsTampering(t *testing.T) {
	var plain bytes.Buffer
	zw := zip.NewWriter(&plain)
	w, _ := zw.Create("nwc.db")
	_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
	_ = zw.Close()

	var enc bytes.Buffer
	ew, _ := encryptingWriter(&enc, "correct horse")
	_, _ = ew.Write(plain.Bytes())
	_ = ew.Close()

	ct := append([]byte(nil), enc.Bytes()...)
	ct[len(ct)/2] ^= 0x01
	if _, err := decryptingReader(bytes.NewReader(ct), "correct horse"); err == nil {
		t.Fatal("AEAD must reject tampered ciphertext")
	}
}

func TestBackupStillImportsLegacyCTR(t *testing.T) {
	scheme := backupCiphers[1]
	var plain bytes.Buffer
	zw := zip.NewWriter(&plain)
	w, _ := zw.Create("nwc.db")
	_, _ = w.Write([]byte("legacy"))
	_ = zw.Close()

	salt := bytes.Repeat([]byte{1}, scheme.saltSize)
	iv := bytes.Repeat([]byte{2}, aes.BlockSize)
	key, err := scheme.deriveKey("pw", salt)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	var enc bytes.Buffer
	_, _ = enc.Write(salt)
	_, _ = enc.Write(iv)
	sw := &cipher.StreamWriter{S: scheme.newStream(block, iv), W: &enc}
	_, _ = sw.Write(plain.Bytes())

	r, err := decryptingReader(bytes.NewReader(enc.Bytes()), "pw")
	if err != nil {
		t.Fatalf("legacy CTR must still import: %v", err)
	}
	out, _ := io.ReadAll(r)
	if !bytes.Equal(out, plain.Bytes()) {
		t.Fatal("legacy mismatch")
	}
}
