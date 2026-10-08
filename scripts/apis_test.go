package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeAPIsEnablesOnlyMissingServices(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, want string
		failList            bool
	}{
		{"missing tasks", "run.googleapis.com\nfirestore.googleapis.com\ncloudbuild.googleapis.com\nartifactregistry.googleapis.com\nsecretmanager.googleapis.com\niam.googleapis.com\naiplatform.googleapis.com\niap.googleapis.com", "services enable cloudtasks.googleapis.com --project=test-project", false},
		{"already enabled", "run.googleapis.com\nfirestore.googleapis.com\ncloudbuild.googleapis.com\nartifactregistry.googleapis.com\nsecretmanager.googleapis.com\niam.googleapis.com\naiplatform.googleapis.com\niap.googleapis.com\ncloudtasks.googleapis.com", "", false},
		{"listing denied", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"scripts", "bin"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"scripts/common.sh", "scripts/apis.sh", "Makefile"} {
				data, err := os.ReadFile(filepath.Join("..", name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			fake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GCLOUD_LOG"
if [[ "$1 $2" == 'services list' ]]; then
  if [[ "$FAIL_LIST" == true ]]; then exit 1; fi
  printf '%s\n' "$ENABLED_SERVICES"
fi
`
			if err := os.WriteFile(filepath.Join(root, "bin/gcloud"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "calls")
			cmd := exec.Command("make", "apis")
			cmd.Dir = root
			fail := "false"
			if tc.failList {
				fail = "true"
			}
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "PROJECT_ID=test-project", "SERVICE=shiori", "REGION=asia-northeast1", "GCLOUD_LOG="+log, "ENABLED_SERVICES="+tc.enabled, "FAIL_LIST="+fail)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.failList {
				t.Fatalf("make apis: %v\n%s", err, output)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(data)
			if tc.want == "" {
				if strings.Contains(calls, "services enable") {
					t.Fatalf("unexpected enable: %s", calls)
				}
			} else if !strings.Contains(calls, tc.want+"\n") {
				t.Fatalf("wrong API selection: %s", calls)
			}
		})
	}
}
