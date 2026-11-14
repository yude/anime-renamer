package fileops

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockExcludesConcurrentOperationAndReusesUnheldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.lock")
	release, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Lock(path); err == nil {
		_ = second()
		t.Fatal("two writers acquired the same file")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("completed operation left a lock file")
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	release, err = Lock(path)
	if err != nil {
		t.Fatalf("an unheld lock from a prior process prevented recovery: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockReleasedOnProcessExit(t *testing.T) {
	if path := os.Getenv("ANIME_RENAMER_TEST_LOCK_PATH"); path != "" {
		if _, err := Lock(path); err != nil {
			os.Exit(1)
		}
		// Exit without cleanup, as when an operation is forcibly terminated.
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "terminated.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedOnProcessExit$")
	cmd.Env = append(os.Environ(), "ANIME_RENAMER_TEST_LOCK_PATH="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lock holder: %v: %s", err, output)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("terminated process should have left its lock file: %v", err)
	}
	release, err := Lock(path)
	if err != nil {
		t.Fatalf("OS lock was not released on process exit: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
