package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexesDoesNotRecreateExistingIndexes(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{scriptsDir, binDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"common.sh", "indexes.sh"} {
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scriptsDir, name), contents, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(root, "gcloud.log")
	fakeGcloud := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GCLOUD_LOG"
if [[ "$1 $2 $3 $4" == 'firestore indexes composite list' ]]; then
  jq -c '.indexes | map(.name = ("projects/test-project/databases/(default)/collectionGroups/" + .collectionGroup + "/indexes/1") | del(.collectionGroup))' "$INDEXES_JSON"
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte(fakeGcloud), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(scriptsDir, "indexes.sh"))
	fixture, err := filepath.Abs("../firestore.indexes.json")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "GCLOUD_LOG="+logPath,
		"INDEXES_JSON="+fixture, "PROJECT_ID=test-project", "SERVICE=shiori")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("indexes.sh failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "composite create") {
		t.Fatalf("existing indexes were recreated:\n%s", calls)
	}
}

func TestIndexesCreatesMissingIndexes(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{scriptsDir, binDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"common.sh", "indexes.sh"} {
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scriptsDir, name), contents, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(root, "gcloud.log")
	fakeGcloud := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GCLOUD_LOG"
if [[ "$1 $2 $3 $4" == 'firestore indexes composite list' ]]; then printf '[]\n'; fi
`
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte(fakeGcloud), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(scriptsDir, "indexes.sh"))
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "GCLOUD_LOG="+logPath,
		"PROJECT_ID=test-project", "SERVICE=shiori")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("indexes.sh failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(calls), "composite create"); got != 6 {
		t.Fatalf("create calls = %d, want 6; calls:\n%s", got, calls)
	}
}
