package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getAlby/hub/logger"
)

func init() { logger.Init("") }

func fakeGlcli(t *testing.T, supportsBackup bool) string {
	dir := t.TempDir()
	help := "Usage: glcli signer run [OPTIONS]"
	if supportsBackup {
		help += "\n      --backup-path <BACKUP_PATH>"
	}
	script := "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = --help ]; then echo '" + help + "'; exit 0; fi; if [ \"$a\" = --version ]; then echo 'gl-cli test'; exit 0; fi; done\nexec sleep 300\n"
	p := filepath.Join(dir, "glcli")
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckGlcliCompat(t *testing.T) {
	if err := checkGlcliCompat(fakeGlcli(t, false)); err == nil || !strings.Contains(err.Error(), "0.1.3") {
		t.Fatalf("old glcli must be rejected with a clear error, got %v", err)
	}
	if err := checkGlcliCompat(fakeGlcli(t, true)); err != nil {
		t.Fatalf("compatible glcli rejected: %v", err)
	}
}

func TestSignerSupervisorLifecycleAndPidReuse(t *testing.T) {
	dataDir := t.TempDir()
	s := NewGreenlightSignerService()
	// stale pid file pointing at THIS test process (pid reuse simulation):
	// the supervisor must neither report it alive nor kill it.
	_ = os.WriteFile(filepath.Join(dataDir, "signer.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	s.dataDir = dataDir
	s.pidPath = filepath.Join(dataDir, "signer.pid")
	if s.processAlive() {
		t.Fatal("stale pid file must not count as alive")
	}
	s.glcli = fakeGlcli(t, true)
	s.network = "testnet"
	if err := s.spawn(); err != nil {
		t.Fatal(err)
	}
	if !s.processAlive() {
		t.Fatal("spawned signer should be alive")
	}
	s.killSigner()
	if s.processAlive() {
		t.Fatal("signer should be dead after kill")
	}
	// we are still alive (the reused pid was not killed)
}

func TestSignerSupervisorRespawn(t *testing.T) {
	dataDir := t.TempDir()
	s := NewGreenlightSignerService()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, dataDir, "testnet", fakeGlcli(t, true), nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !s.processAlive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !s.processAlive() {
		t.Fatal("not started")
	}
	s.Stop()
	if s.processAlive() {
		t.Fatal("still alive after stop")
	}
}
