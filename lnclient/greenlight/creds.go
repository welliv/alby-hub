package greenlight

import (
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"google.golang.org/protobuf/encoding/protowire"
)

// deviceCreds is the decoded greenlight Device credentials blob
// (credentials.gfs): field 2 cert, 3 key, 4 ca, 5 rune.
type deviceCreds struct {
	Cert, Key, CA, Rune []byte
}

// parseDeviceCreds decodes credentials.gfs (pure Go replacement of the
// former extract_creds.py; no Python runtime dependency).
func parseDeviceCreds(data []byte) (*deviceCreds, error) {
	c := &deviceCreds{}
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, fmt.Errorf("creds: bad tag: %w", protowire.ParseError(n))
		}
		data = data[n:]
		switch typ {
		case protowire.BytesType:
			v, m := protowire.ConsumeBytes(data)
			if m < 0 {
				return nil, fmt.Errorf("creds: bad field %d: %w", num, protowire.ParseError(m))
			}
			switch num {
			case 2:
				c.Cert = append([]byte(nil), v...)
			case 3:
				c.Key = append([]byte(nil), v...)
			case 4:
				c.CA = append([]byte(nil), v...)
			case 5:
				c.Rune = append([]byte(nil), v...)
			}
			data = data[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, data)
			if m < 0 {
				return nil, fmt.Errorf("creds: bad field %d: %w", num, protowire.ParseError(m))
			}
			data = data[m:]
		}
	}
	if len(c.Cert) == 0 || len(c.Key) == 0 || len(c.CA) == 0 {
		return nil, errors.New("creds blob missing required fields (cert/key/ca)")
	}
	return c, nil
}

// nodeDomainFromCert derives gl1<bech32m(pubkey)>.nodes.gl.blckstrm.com
// from the device cert CN (/users/<hex pubkey>[/<slot>]).
func nodeDomainFromCert(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("client cert: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("client cert: %w", err)
	}
	for _, p := range strings.Split(cert.Subject.CommonName, "/") {
		if len(p) != 66 {
			continue
		}
		pub, err := hex.DecodeString(p)
		if err != nil || (pub[0] != 2 && pub[0] != 3) {
			continue
		}
		conv, err := bech32.ConvertBits(pub, 8, 5, true)
		if err != nil {
			return "", err
		}
		enc, err := bech32.EncodeM("gl", conv)
		if err != nil {
			return "", err
		}
		return enc + ".nodes.gl.blckstrm.com", nil
	}
	return "", errors.New("client cert CN carries no node pubkey")
}

// extractDeviceCreds writes ca.pem, client.pem, client-key.pem, rune
// (0600, dir 0700) and returns the node URI.
func extractDeviceCreds(credsBlob, outDir string) (string, error) {
	data, err := os.ReadFile(credsBlob)
	if err != nil {
		return "", err
	}
	c, err := parseDeviceCreds(data)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(outDir, 0o700); err != nil {
		return "", err
	}
	for name, content := range map[string][]byte{"client.pem": c.Cert, "client-key.pem": c.Key, "ca.pem": c.CA, "rune": c.Rune} {
		p := filepath.Join(outDir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			return "", err
		}
		if err := os.Chmod(p, 0o600); err != nil {
			return "", err
		}
	}
	domain, err := nodeDomainFromCert(c.Cert)
	if err != nil {
		return "", err
	}
	return domain + ":443", nil
}

// hardenPerms enforces 0700 on dirs and 0600 on files under dataDir.
// glcli writes credentials.gfs world-readable; it holds the device key.
func hardenPerms(dataDir string) error {
	return filepath.WalkDir(dataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.Chmod(p, 0o700)
		}
		return os.Chmod(p, 0o600)
	})
}
