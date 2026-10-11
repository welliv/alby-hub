package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHardenWorkdirPerms(t *testing.T) {
	wd := filepath.Join(t.TempDir(), "hub")
	db := filepath.Join(wd, "nwc.db")
	if err := hardenWorkdirPerms(wd, db); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{wd: 0o700, filepath.Join(wd, "log"): 0o700, db: 0o600} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != want {
			t.Fatalf("%s: want %o got %v (%v)", p, want, st.Mode().Perm(), err)
		}
	}
	// existing world-readable DB + wal get tightened
	os.Chmod(db, 0o644)
	os.WriteFile(db+"-wal", nil, 0o644)
	if err := hardenWorkdirPerms(wd, db); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{db, db + "-wal"} {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("%s not tightened: %v", p, st.Mode().Perm())
		}
	}
	if err := hardenWorkdirPerms(wd, "file::memory:?cache=shared"); err != nil {
		t.Fatal(err)
	}
}
