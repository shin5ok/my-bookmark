package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretsCreatesMissingSecretBeforeAddingVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"common.sh", "secrets.sh"} {
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
if [[ "$1 $2" == "secrets describe" ]]; then exit 1; fi
if [[ "$1 $2 $3" == "secrets versions add" ]]; then cat >/dev/null; fi
`
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte(fakeGcloud), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(scriptsDir, "secrets.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GCLOUD_LOG="+logPath,
		"PROJECT_ID=test-project",
		"SERVICE=shiori",
		"GOOGLE_CLIENT_SECRET=test-secret-value",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("secrets.sh failed: %v\n%s", err, output)
	}

	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(contents)
	create := "secrets create shiori-google-client-secret --replication-policy=automatic --project=test-project"
	addVersion := "secrets versions add shiori-google-client-secret --data-file=- --project=test-project"
	if !strings.Contains(calls, create) {
		t.Fatalf("missing Secret creation call; calls:\n%s", calls)
	}
	if !strings.Contains(calls, addVersion) {
		t.Fatalf("missing Secret version call; calls:\n%s", calls)
	}
	if strings.Index(calls, create) > strings.Index(calls, addVersion) {
		t.Fatalf("Secret version was added before Secret creation; calls:\n%s", calls)
	}
}

func TestSecretsSkipsUnchangedValue(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"common.sh", "secrets.sh"} {
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
case "$1 $2 $3" in
  'secrets versions list') printf 'versions/1\n' ;;
  'secrets versions access') printf '%s' "$EXISTING_SECRET" ;;
  'secrets versions add') cat >/dev/null ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte(fakeGcloud), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, existing string
		wantAdd        bool
	}{
		{name: "same", existing: "test-secret-value"},
		{name: "changed", existing: "old-secret-value", wantAdd: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(logPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", filepath.Join(scriptsDir, "secrets.sh"))
			cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "GCLOUD_LOG="+logPath,
				"PROJECT_ID=test-project", "SERVICE=shiori", "GOOGLE_CLIENT_SECRET=test-secret-value", "EXISTING_SECRET="+tc.existing)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("secrets.sh failed: %v\n%s", err, output)
			}
			calls, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			gotAdd := strings.Contains(string(calls), "secrets versions add")
			if gotAdd != tc.wantAdd {
				t.Fatalf("add version = %t, want %t; calls:\n%s", gotAdd, tc.wantAdd, calls)
			}
		})
	}
}
