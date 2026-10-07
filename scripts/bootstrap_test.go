package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapSkipsConfiguredResources(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{scriptsDir, binDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"common.sh", "bootstrap.sh", "indexes.sh"} {
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scriptsDir, name), contents, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs("../firestore.indexes.json")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "gcloud.log")
	fakeGcloud := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GCLOUD_LOG"
case "$1 $2 $3 $4" in
  'services list --enabled '*) printf '%s\n' run.googleapis.com firestore.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com iam.googleapis.com aiplatform.googleapis.com iap.googleapis.com ;;
  'projects get-iam-policy '*) printf '%s\n' '{"bindings":[{"role":"roles/datastore.user","members":["serviceAccount:shiori-runtime@test-project.iam.gserviceaccount.com"]},{"role":"roles/aiplatform.user","members":["serviceAccount:shiori-runtime@test-project.iam.gserviceaccount.com"]},{"role":"roles/run.builder","members":["serviceAccount:shiori-build@test-project.iam.gserviceaccount.com"]}]}' ;;
  'secrets get-iam-policy '*) printf '%s\n' '{"bindings":[{"role":"roles/secretmanager.secretAccessor","members":["serviceAccount:shiori-runtime@test-project.iam.gserviceaccount.com"]}]}' ;;
  'firestore fields ttls list'*)
    for arg in "$@"; do
      case "$arg" in --collection-group=*) printf 'projects/test-project/databases/(default)/collectionGroups/%s/fields/expires_at\n' "${arg#*=}" ;; esac
    done ;;
  'firestore indexes composite list') jq -c '.indexes | map(.name = ("projects/test-project/databases/(default)/collectionGroups/" + .collectionGroup + "/indexes/1") | del(.collectionGroup))' "$INDEXES_JSON" ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte(fakeGcloud), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(scriptsDir, "bootstrap.sh"))
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "GCLOUD_LOG="+logPath,
		"INDEXES_JSON="+fixture, "PROJECT_ID=test-project", "SERVICE=shiori")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap.sh failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"services enable", "add-iam-policy-binding", "ttls update", "composite create", "databases create", "service-accounts create", "secrets create"} {
		if strings.Contains(string(calls), operation) {
			t.Errorf("configured resource was changed by %q:\n%s", operation, calls)
		}
	}
}
