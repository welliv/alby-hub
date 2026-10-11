package greenlight

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/getAlby/hub/logger"
	"github.com/tyler-smith/go-bip39"
)

const (
	seedFileName        = "hsm_secret"
	credentialsFileName = "credentials.gfs"
	deviceCredsDirName  = "device-creds"
)

// reMnemonic matches the rest of a "mnemonic: ..." line (kept for tests;
// sanitizeGlcliOutput is the full filter used on every glcli output).
var reMnemonic = regexp.MustCompile(`mnemonic:\s*\S.*`)

// MnemonicToSeed32 matches gl-cli: mnemonic.to_seed("")[0..32], 12 words only.
func MnemonicToSeed32(mnemonic string) ([]byte, error) {
	mnemonic = strings.TrimSpace(mnemonic)
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid bip39 mnemonic")
	}
	if words := strings.Fields(mnemonic); len(words) != 12 {
		return nil, fmt.Errorf("greenlight requires a 12-word mnemonic, got %d", len(words))
	}
	full := bip39.NewSeed(mnemonic, "")
	seed := make([]byte, 32)
	copy(seed, full[:32])
	return seed, nil
}

// WriteSeedFile writes raw 32-byte seed as hsm_secret (0600).
func WriteSeedFile(dataDir string, seed []byte) error {
	if len(seed) != 32 {
		return fmt.Errorf("seed must be 32 bytes, got %d", len(seed))
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, seedFileName), seed, 0o600)
}

func resolveGlcli(glcliPath string) (string, error) {
	if glcliPath == "" {
		glcliPath = "glcli"
	}
	if bin, err := exec.LookPath(glcliPath); err == nil {
		return bin, nil
	}
	for _, p := range []string{"/root/.cargo/bin/glcli", "/usr/local/bin/glcli"} {
		if _, e := os.Stat(p); e == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("glcli not found (set GREENLIGHT_GLCLI_PATH)")
}

// ensureSeedFile writes the seed derived from the mnemonic ONLY when no
// hsm_secret exists yet. The existing file is authoritative: the node's
// identity derives from it, and overwriting it with a different mnemonic
// would silently desync the signer from the node (every signing request
// would be rejected). Recovery works because a fresh data dir has no file.
func ensureSeedFile(dataDir string, seed []byte) error {
	seedPath := filepath.Join(dataDir, seedFileName)
	if _, err := os.Stat(seedPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat hsm_secret: %w", err)
	}
	return WriteSeedFile(dataDir, seed)
}

// ShredSeedFile overwrites hsm_secret with zeros then removes it.
// Missing file is success: lock must be idempotent.
func ShredSeedFile(dataDir string) error {
	seedPath := filepath.Join(dataDir, seedFileName)
	st, err := os.Stat(seedPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat hsm_secret: %w", err)
	}
	f, err := os.OpenFile(seedPath, os.O_WRONLY, 0)
	if err != nil {
		_ = os.Remove(seedPath)
		return fmt.Errorf("open hsm_secret for shred: %w", err)
	}
	zeros := make([]byte, st.Size())
	if _, err := f.Write(zeros); err != nil {
		_ = f.Close()
		_ = os.Remove(seedPath)
		return fmt.Errorf("overwrite hsm_secret: %w", err)
	}
	_ = f.Sync()
	_ = f.Close()
	if err := os.Remove(seedPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove hsm_secret: %w", err)
	}
	return nil
}

// EnsureProvisioned writes seed from mnemonic, registers/recovers via glcli if needed,
// extracts device PEMs, and returns (deviceCredsDir, nodeURI).
func EnsureProvisioned(dataDir, network, glcliPath, nobodyCrt, nobodyKey, mnemonic string) (credsDir, nodeURI string, err error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", "", err
	}

	seed, err := MnemonicToSeed32(mnemonic)
	if err != nil {
		return "", "", err
	}
	if err := ensureSeedFile(dataDir, seed); err != nil {
		return "", "", fmt.Errorf("write hsm_secret: %w", err)
	}

	credsBlob := filepath.Join(dataDir, credentialsFileName)
	needRegister := true
	if st, err := os.Stat(credsBlob); err == nil && st.Size() > 0 {
		needRegister = false
	}

	bin, err := resolveGlcli(glcliPath)
	if err != nil {
		return "", "", err
	}

	if nobodyCrt == "" {
		nobodyCrt = os.Getenv("GL_NOBODY_CRT")
	}
	if nobodyKey == "" {
		nobodyKey = os.Getenv("GL_NOBODY_KEY")
	}

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		env := os.Environ()
		if nobodyCrt != "" {
			// guard against env-injection: reject paths with newlines
			if strings.ContainsAny(nobodyCrt, "\n\r") || strings.ContainsAny(nobodyKey, "\n\r") {
				return "", fmt.Errorf("GL_NOBODY_CRT/KEY must not contain newlines")
			}
			env = append(env, "GL_NOBODY_CRT="+nobodyCrt)
		}
		if nobodyKey != "" {
			env = append(env, "GL_NOBODY_KEY="+nobodyKey)
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		// strip seed/mnemonic from output before it is logged, returned
		// or embedded in an error — callers only ever see sanitized text
		sanitized := strings.TrimSpace(sanitizeGlcliOutput(string(out)))
		if err != nil {
			return sanitized, fmt.Errorf("%w: %s", err, sanitized)
		}
		// glcli (<= 0.1.3) prints RPC failures but still exits 0.
		if glcliReportedFailure(sanitized) {
			return sanitized, fmt.Errorf("glcli reported failure (exit 0): %s", sanitized)
		}
		return sanitized, nil
	}

	if needRegister {
		if nobodyCrt == "" || nobodyKey == "" {
			return "", "", fmt.Errorf("GL_NOBODY_CRT and GL_NOBODY_KEY required to register a greenlight node")
		}
		logger.Logger.Info("Registering greenlight node via glcli")
		out, regErr := run("-d", dataDir, "-n", network, "scheduler", "register")
		// glcli can exit 0 while still printing "Failed to register node"
		// (node already exists). Treat missing credentials.gfs as failure
		// and fall through to recover — that is the mnemonic reinstall path.
		if _, statErr := os.Stat(credsBlob); statErr != nil {
			if regErr == nil {
				regErr = fmt.Errorf("register left no credentials.gfs: %s", strings.TrimSpace(out))
			}
		}
		if regErr != nil {
			logger.Logger.WithError(regErr).Warn("register failed, trying recover")
			out2, recErr := run("-d", dataDir, "-n", network, "scheduler", "recover")
			if recErr != nil {
				return "", "", fmt.Errorf("register failed (%v); recover failed: %w\nregister: %s\nrecover: %s", regErr, recErr, out, out2) // outputs are sanitized
			}
			logger.Logger.Info("Recovered greenlight device credentials")
		} else {
			logger.Logger.WithField("out", out).Info("Registered greenlight node") // sanitized
		}
	}

	if _, err := os.Stat(credsBlob); err != nil {
		return "", "", fmt.Errorf("credentials.gfs missing in %s", dataDir)
	}
	if err := os.Chmod(credsBlob, 0o600); err != nil {
		return "", "", fmt.Errorf("chmod credentials.gfs: %w", err)
	}

	// schedule (wake)
	if _, err := run("-d", dataDir, "-n", network, "scheduler", "schedule"); err != nil {
		logger.Logger.WithError(err).Warn("glcli schedule failed (node domain may still wake on connect)")
	}

	credsDir = filepath.Join(dataDir, deviceCredsDirName)
	nodeURI, err = extractDeviceCreds(credsBlob, credsDir)
	if err != nil {
		return "", "", fmt.Errorf("extract device credentials: %w", err)
	}
	if err := hardenPerms(dataDir); err != nil {
		return "", "", fmt.Errorf("harden greenlight data dir permissions: %w", err)
	}
	logger.Logger.WithField("node_uri", nodeURI).Info("Extracted greenlight device credentials")

	// Required PEMs
	for _, f := range []string{"ca.pem", "client.pem", "client-key.pem"} {
		if _, err := os.Stat(filepath.Join(credsDir, f)); err != nil {
			return "", "", fmt.Errorf("missing %s after extract", f)
		}
	}
	return credsDir, nodeURI, nil
}

var reGrpcURI = regexp.MustCompile(`grpc_uri:\s*"([^"]+)"`)

// ScheduleNodeURIForRegtest asks the scheduler (via glcli) where the node
// runs. DEV ONLY: used solely for network "regtest" against a local
// gl-testserver whose nodes get random localhost ports per schedule. On
// real networks the node URI is derived from the device certificate.
func ScheduleNodeURIForRegtest(dataDir, network, glcliPath string) (string, error) {
	if network != "regtest" {
		return "", fmt.Errorf("scheduler node-uri override is regtest-only")
	}
	bin := glcliPath
	if bin == "" {
		bin = "glcli"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-d", dataDir, "-n", network, "scheduler", "schedule").CombinedOutput() // #nosec G204 -- fixed argv
	if err != nil {
		return "", fmt.Errorf("glcli scheduler schedule: %w: %s", err, sanitizeGlcliOutput(string(out)))
	}
	m := reGrpcURI.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("no grpc_uri in schedule output")
	}
	return strings.TrimPrefix(strings.TrimPrefix(m[1], "https://"), "http://"), nil
}

// glcliReportedFailure detects glcli failures that exit with status 0
// (observed: "Failed to register node: status: ...", "Failed to export node",
// "Error calling method Xpay: RpcError ...").
func glcliReportedFailure(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "Failed to ") || strings.HasPrefix(l, "Error calling method ") ||
			(strings.HasPrefix(l, "status: ") && strings.Contains(l, "message:")) {
			return true
		}
	}
	return false
}
