package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/logging"
)

func logTo(t *testing.T, file, level string) {
	t.Helper()
	LogFile, LogLevel = file, level
	t.Cleanup(func() {
		LogFile, LogLevel = "", ""
		logging.Close()
	})
}

func TestStartLoggingWritesWhereTheFlagSays(t *testing.T) {
	where := filepath.Join(t.TempDir(), "runs", "this-one.log")
	logTo(t, where, "")

	if err := StartLogging(nil, nil); err != nil {
		t.Fatalf("StartLogging() error = %v", err)
	}
	if got := logging.Path(); got != where {
		t.Errorf("Path() = %q, want %q", got, where)
	}
	logging.L().Info("hello")
	if _, err := os.Stat(where); err != nil {
		t.Errorf("stat %s: %v", where, err)
	}
}

func TestStartLoggingRefusesALevelTheFlagGotWrong(t *testing.T) {
	logTo(t, filepath.Join(t.TempDir(), "a.log"), "chatty")

	if err := StartLogging(nil, nil); err == nil {
		t.Fatal("StartLogging() error = nil, want the bad --log-level reported")
	}
}

func TestABadLevelInTheEnvironmentStillLeavesALog(t *testing.T) {
	t.Setenv("ION_LOG_LEVEL", "chatty")
	logTo(t, filepath.Join(t.TempDir(), "a.log"), "")

	err := StartLogging(nil, nil)
	if err == nil {
		t.Error("StartLogging() error = nil, want the bad ION_LOG_LEVEL reported")
	}
	if !logging.On() {
		t.Error("nothing is being logged; a bad environment variable must not cost the run its log")
	}
}

func TestLoggingOffWritesNothing(t *testing.T) {
	dir := t.TempDir()
	logTo(t, filepath.Join(dir, "a.log"), "off")

	if err := StartLogging(nil, nil); err != nil {
		t.Fatalf("StartLogging() error = %v", err)
	}
	logging.L().Error("not this either")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%q holds %d files, want none", dir, len(entries))
	}
}

func TestTheCredentialPathReachesTheLog(t *testing.T) {
	where := filepath.Join(t.TempDir(), "a.log")
	logTo(t, where, "")
	if err := StartLogging(nil, nil); err != nil {
		t.Fatalf("StartLogging() error = %v", err)
	}

	f := BaseFlags()
	f.Token, f.TSGID = "scm-token-value-0001", "1436146912"
	if _, err := NewClient(context.Background(), nil, f, io.Discard); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	b, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{"credential path", "using SCM_TOKEN", "1436146912"} {
		if !strings.Contains(body, want) {
			t.Errorf("the log never says %q, which is the one line the TUI used to throw away:\n%s", want, body)
		}
	}
	if strings.Contains(body, f.Token) {
		t.Errorf("the bearer token reached the log:\n%s", body)
	}
}
