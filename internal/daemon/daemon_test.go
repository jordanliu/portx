package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePIDRecord(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "portxd.pid")
	want := pidRecord{
		PID:        1234,
		Executable: "/usr/local/bin/portx",
		Kind:       "portxd",
		StartTime:  123456789,
	}
	if err := writePIDRecord(path, want); err != nil {
		t.Fatalf("writePIDRecord returned error: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record pidRecord
	if err := json.Unmarshal(b, &record); err != nil {
		t.Fatalf("pid record is not JSON: %v", err)
	}
	if record != want {
		t.Fatalf("unexpected pid record: %+v", record)
	}
}

func TestWritePIDRecordRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	if err := writePIDRecord(filepath.Join(t.TempDir(), "invalid.pid"), pidRecord{
		PID:        0,
		Executable: "portx",
		Kind:       "portxd",
		StartTime:  1,
	}); err == nil {
		t.Fatal("writePIDRecord accepted an invalid pid")
	}
}

func TestProcessStartTime(t *testing.T) {
	t.Parallel()

	startTime, err := processStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("processStartTime returned error: %v", err)
	}
	if startTime <= 0 {
		t.Fatalf("processStartTime returned %d", startTime)
	}
}
