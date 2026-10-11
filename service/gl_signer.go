package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/getAlby/hub/events"
	"github.com/getAlby/hub/lnclient/greenlight"
	"github.com/getAlby/hub/logger"
	"github.com/sirupsen/logrus"
)

// GreenlightSignerService supervises `glcli signer run` while the hub is unlocked.
type GreenlightSignerService struct {
	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
	dataDir  string
	network  string
	glcli    string
	pidPath  string
	lastErr  string
	cmd      *exec.Cmd     // the live signer process, when spawned
	done     chan struct{} // closed when cmd exits

	eventPublisher       events.EventPublisher // nil in external-signer mode
	lastPublishedHealthy bool
}

func NewGreenlightSignerService() *GreenlightSignerService {
	return &GreenlightSignerService{lastPublishedHealthy: false}
}

func (s *GreenlightSignerService) Start(ctx context.Context, dataDir, network, glcliPath string, eventPublisher events.EventPublisher) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.dataDir = dataDir
	s.network = network
	if s.network == "" {
		s.mu.Unlock()
		return fmt.Errorf("network required (set GREENLIGHT_NETWORK)")
	}
	s.glcli = glcliPath
	s.pidPath = filepath.Join(dataDir, "signer.pid")
	s.eventPublisher = eventPublisher
	s.lastPublishedHealthy = false
	child, cancel := context.WithCancel(ctx)
	s.cancelFn = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.supervise(child)
	}()
	logger.Logger.Info("Greenlight signer service started")
	return nil
}

func (s *GreenlightSignerService) Stop() {
	s.mu.Lock()
	dataDir := s.dataDir
	if !s.running {
		s.mu.Unlock()
		s.shredSeed(dataDir)
		return
	}
	s.running = false
	cancel := s.cancelFn
	s.cancelFn = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.killSigner()
	s.wg.Wait()
	s.shredSeed(dataDir)
	logger.Logger.Info("Greenlight signer service stopped")
}

func (s *GreenlightSignerService) shredSeed(dataDir string) {
	if dataDir == "" {
		return
	}
	if err := greenlight.ShredSeedFile(dataDir); err != nil {
		logger.Logger.WithError(err).Warn("failed to shred greenlight hsm_secret")
	}
}

func (s *GreenlightSignerService) IsRunning() bool {
	return s.processAlive()
}

func (s *GreenlightSignerService) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *GreenlightSignerService) setErr(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.mu.Unlock()
}

func (s *GreenlightSignerService) supervise(ctx context.Context) {
	if err := s.ensure(ctx); err != nil {
		s.setErr(err.Error())
		logger.Logger.WithError(err).Error("greenlight signer initial start failed (will retry)")
	} else {
		s.setErr("")
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.killSigner()
			return
		case <-t.C:
			if err := s.ensure(ctx); err != nil {
				s.setErr(err.Error())
				s.publishHealth(false, err.Error())
				logger.Logger.WithError(err).Warn("greenlight signer unhealthy, retrying")
			} else if s.LastError() != "" {
				// recovered: error was set previously but ensure() now succeeds
				s.setErr("")
				s.publishHealth(true, "")
				logger.Logger.Info("greenlight signer healthy again")
			}
		}
	}
}

func (s *GreenlightSignerService) publishHealth(healthy bool, lastErr string) {
	if s.eventPublisher == nil {
		return
	}
	s.mu.Lock()
	if s.lastPublishedHealthy == healthy {
		s.mu.Unlock()
		return // no transition
	}
	s.lastPublishedHealthy = healthy
	s.mu.Unlock()
	props := map[string]interface{}{"healthy": healthy}
	if !healthy {
		props["error"] = lastErr
	}
	s.eventPublisher.Publish(&events.Event{
		Event:      "nwc_gl_signer_health",
		Properties: props,
	})
}

func (s *GreenlightSignerService) ensure(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.processAlive() {
		return nil
	}
	return s.spawn()
}

func (s *GreenlightSignerService) resolveBinary() (string, error) {
	if s.glcli != "" {
		if p, err := exec.LookPath(s.glcli); err == nil {
			return p, nil
		}
		if _, err := os.Stat(s.glcli); err == nil {
			return s.glcli, nil
		}
	}
	if p, err := exec.LookPath("glcli"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/root/.cargo/bin/glcli", "/usr/local/bin/glcli"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("glcli not found")
}

// checkGlcliCompat verifies the glcli binary supports the flags the hub
// relies on. gl-cli 0.1.1 (crates.io) lacks --backup-path; without it the
// signer would exit immediately and the node would be unable to sign.
func checkGlcliCompat(bin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "signer", "run", "--help").CombinedOutput()
	if !strings.Contains(string(out), "--backup-path") {
		ver, _ := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
		return fmt.Errorf("unsupported glcli %q (%s): signer run lacks --backup-path; install gl-cli >= 0.1.3 from https://github.com/Blockstream/greenlight", bin, strings.TrimSpace(string(ver)))
	}
	return nil
}

// reapOrphan kills a signer left behind by a previous hub process (hub
// crash), but only if the pid really is our glcli for this data dir — a
// stale pid file may point at an unrelated, reused pid.
func (s *GreenlightSignerService) reapOrphan() {
	data, err := os.ReadFile(s.pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	_ = os.Remove(s.pidPath)
	if err != nil || pid <= 1 {
		return
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return
	}
	c := string(cmdline)
	if strings.Contains(c, "glcli") && strings.Contains(c, s.dataDir) && strings.Contains(c, "signer") {
		logger.Logger.WithField("pid", pid).Warn("killing orphaned greenlight signer from a previous hub run")
		_ = syscall.Kill(-pid, syscall.SIGKILL) // own process group (Setsid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (s *GreenlightSignerService) spawn() error {
	bin, err := s.resolveBinary()
	if err != nil {
		return err
	}
	if err := checkGlcliCompat(bin); err != nil {
		return err
	}
	s.reapOrphan()
	backupPath := filepath.Join(s.dataDir, "backup.json")
	args := []string{
		"-d", s.dataDir,
		"-n", s.network,
		"signer", "run",
		"--backup-path", backupPath,
	}
	logPath := filepath.Join(s.dataDir, "signer.log")
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open signer log: %w", err)
	}
	_ = logF.Chmod(0o600)
	cmd := exec.Command(bin, args...) // #nosec G204 -- resolved glcli path, fixed argv, no shell
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logF.Close()
		return fmt.Errorf("spawn glcli signer: %w", err)
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(s.pidPath, []byte(strconv.Itoa(pid)), 0o600) // informational + orphan reaping only
	done := make(chan struct{})
	s.mu.Lock()
	s.cmd = cmd
	s.done = done
	s.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		_ = logF.Close()
		close(done)
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
			s.done = nil
		}
		s.mu.Unlock()
	}()
	logger.Logger.WithFields(logrus.Fields{"pid": pid, "data_dir": s.dataDir, "log": logPath}).Info("glcli signer spawned")
	select {
	case <-done:
		tail, _ := os.ReadFile(logPath)
		msg := strings.TrimSpace(greenlight.SanitizeOutput(string(tail)))
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return fmt.Errorf("signer exited immediately after spawn: %s", msg)
	case <-time.After(800 * time.Millisecond):
	}
	return nil
}

// processAlive reports whether the exact process we spawned is running.
// Liveness is tracked through the process handle and its exit channel,
// never through the pid file (pids get reused).
func (s *GreenlightSignerService) processAlive() bool {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

func (s *GreenlightSignerService) killSigner() {
	s.mu.Lock()
	cmd, done := s.cmd, s.done
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil || done == nil {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
	_ = os.Remove(s.pidPath)
}
