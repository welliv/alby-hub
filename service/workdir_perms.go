package service

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hardenWorkdirPerms keeps the hub's secrets (encrypted mnemonic and app
// keys in the sqlite DB, logs) private to the hub user: workdir and log dir
// 0700, DB (+ -wal/-shm, which sqlite creates with the DB's mode) 0600.
func hardenWorkdirPerms(workdir, databaseURI string) error {
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(workdir, 0o700); err != nil {
		return err
	}
	logDir := filepath.Join(workdir, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(logDir, 0o700); err != nil {
		return err
	}
	if databaseURI == "" || strings.HasPrefix(databaseURI, "file:") || strings.Contains(databaseURI, "://") {
		return nil // URIs (memory, postgres) are not plain files
	}
	if _, err := os.Stat(databaseURI); errors.Is(err, fs.ErrNotExist) {
		f, err := os.OpenFile(databaseURI, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		return f.Close()
	}
	for _, p := range []string{databaseURI, databaseURI + "-wal", databaseURI + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
