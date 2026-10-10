package greenlight

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"google.golang.org/protobuf/encoding/protowire"
)

func testCertPEM(t *testing.T, cn string) []byte {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestExtractDeviceCreds(t *testing.T) {
	priv, _ := btcec.NewPrivateKey()
	pub := hex.EncodeToString(priv.PubKey().SerializeCompressed())
	cert := testCertPEM(t, "GL /users/"+pub)
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	for i, v := range [][]byte{cert, []byte("KEY"), []byte("CA"), []byte("RUNE")} {
		b = protowire.AppendTag(b, protowire.Number(i+2), protowire.BytesType)
		b = protowire.AppendBytes(b, v)
	}
	dir := t.TempDir()
	blob := filepath.Join(dir, "credentials.gfs")
	if err := os.WriteFile(blob, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "device-creds")
	uri, err := extractDeviceCreds(blob, out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "gl1") || !strings.HasSuffix(uri, ".nodes.gl.blckstrm.com:443") {
		t.Fatalf("bad uri %s", uri)
	}
	for _, f := range []string{"client.pem", "client-key.pem", "ca.pem", "rune"} {
		st, err := os.Stat(filepath.Join(out, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s perms: %v %v", f, st.Mode(), err)
		}
	}
	if err := hardenPerms(dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(blob); st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.gfs must be 0600, got %v", st.Mode())
	}
}

// Optional cross-check against a real credentials.gfs (not committed).
func TestExtractDeviceCredsLiveFile(t *testing.T) {
	blob := os.Getenv("GL_TEST_CREDS_GFS")
	want := os.Getenv("GL_TEST_NODE_DOMAIN")
	if blob == "" || want == "" {
		t.Skip("set GL_TEST_CREDS_GFS and GL_TEST_NODE_DOMAIN")
	}
	uri, err := extractDeviceCreds(blob, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if uri != want+":443" {
		t.Fatalf("got %s want %s", uri, want)
	}
}

func TestParseDeviceCredsRejectsGarbage(t *testing.T) {
	if _, err := parseDeviceCreds([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parseDeviceCreds(nil); err == nil {
		t.Fatal("expected missing-field error")
	}
}

func TestSanitizeGlcliOutput(t *testing.T) {
	m := "abandon ability able about above absent absorb abstract absurd abuse access accident"
	for _, in := range []string{
		"New seed generated from mnemonic: " + m,
		"some prefix\n" + m + "\ntrailer",
		"MNEMONIC = " + m,
	} {
		out := sanitizeGlcliOutput(in)
		if strings.Contains(out, "absorb") {
			t.Fatalf("leak: %q", out)
		}
	}
	if got := sanitizeGlcliOutput("Credentials saved at /x"); got != "Credentials saved at /x" {
		t.Fatalf("over-redaction: %q", got)
	}
}

func FuzzParseDeviceCreds(f *testing.F) {
	f.Add([]byte{0x12, 0x01, 0x41})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = parseDeviceCreds(b) })
}

func FuzzSanitize(f *testing.F) {
	f.Add("mnemonic: abandon")
	f.Add("seed phrase: abandon ability able about above absent absorb abstract absurd abuse access accident")
	f.Add("ok line with no secrets")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			t.Skip()
		}
		out := sanitizeGlcliOutput(s)
		if len(out) > 8192 {
			t.Fatalf("output unbounded: %d", len(out))
		}
	})
}

func TestReadSignerLogTailSanitizedAndBounded(t *testing.T) {
	d := t.TempDir()
	body := strings.Repeat("x", 5000) + "\nmnemonic: abandon abandon about\nok line\n"
	if err := os.WriteFile(filepath.Join(d, "signer.log"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := readSignerLogTail(d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 100 || strings.Contains(string(out), "abandon") || !strings.Contains(string(out), "ok line") {
		t.Fatalf("bad tail %q", out)
	}
	if out, err := readSignerLogTail(t.TempDir(), 10); err != nil || len(out) != 0 {
		t.Fatalf("missing log should be empty, got %q %v", out, err)
	}
}
