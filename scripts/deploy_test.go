package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployBuildsImageBeforeDeploying(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"scripts", "bin"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"common.sh", "deploy.sh"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "scripts", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "allow_accounts.yaml"), []byte("domains: [data-cloud.jp]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "go"), []byte("#!/usr/bin/env bash\nif [[ \"$3\" == --validate ]]; then exit 0; fi\nprintf '{\"bindings\":[{\"role\":\"roles/iap.httpsResourceAccessor\",\"members\":[\"domain:data-cloud.jp\"]}]}' > \"$5\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "calls")
	fake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GCLOUD_LOG"
if [[ "$1 $2" == 'projects describe' ]]; then echo 123456789; fi
if [[ "$1 $2 $3" == 'artifacts repositories describe' && "${MISSING_REPO:-}" == 1 ]]; then exit 1; fi
`
	if err := os.WriteFile(filepath.Join(root, "bin", "gcloud"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "deploy.sh"))
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "GCLOUD_LOG="+log,
		"PROJECT_ID=test-project", "SERVICE=shiori", "GOOGLE_CLIENT_ID=test-client")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(data)
	workerDeploy := strings.Index(calls, "run deploy shiori-worker ")
	uiDeploy := strings.Index(calls, "run deploy shiori ")
	if workerDeploy < 0 || uiDeploy < workerDeploy {
		t.Fatalf("worker must be ready before the dispatcher: %s", calls)
	}
	workerCommand := strings.Split(calls[workerDeploy:], "\n")[0]
	if !strings.Contains(workerCommand, "--execution-environment=gen2") {
		t.Fatalf("sandboxed Chromium requires an explicit gen2 worker: %s", workerCommand)
	}
	if !strings.Contains(workerCommand, "--no-allow-unauthenticated --no-iap") || !strings.Contains(workerCommand, "WORKER_ONLY=true") || !strings.Contains(workerCommand, "--timeout=660") || !strings.Contains(workerCommand, "--concurrency=1") {
		t.Fatalf("worker isolation or long request budget missing: %s", workerCommand)
	}
	if !strings.Contains(calls, "tasks queues update shiori-summary") || !strings.Contains(calls, "--member=serviceAccount:shiori-tasks@test-project.iam.gserviceaccount.com --role=roles/run.invoker") || !strings.Contains(calls, "TASKS_QUEUE=projects/test-project/locations/asia-northeast1/queues/shiori-summary") {
		t.Fatalf("task queue or authenticated invocation missing: %s", calls)
	}
	build := strings.Index(calls, "builds submit")
	deploy := strings.Index(calls, "run deploy")
	if build < 0 || deploy < build {
		t.Fatalf("expected Cloud Build before Cloud Run: %s", calls)
	}
	if !strings.Contains(calls[deploy:], "--image=") || strings.Contains(calls[deploy:], "--source=") {
		t.Fatalf("expected Cloud Run to deploy the built image: %s", calls)
	}

	apiDeploy := strings.Index(calls, "run deploy shiori-api ")
	exclusion := strings.Index(calls, "logging sinks update _Default")
	if apiDeploy < 0 || exclusion < 0 || exclusion > apiDeploy || !strings.Contains(calls[apiDeploy:], "API_ONLY=true") || !strings.Contains(calls[apiDeploy:], "--allow-unauthenticated --no-iap") {
		t.Fatalf("API service must be isolated and log exclusion configured first: %s", calls)
	}
	if !strings.Contains(calls[deploy:], "API_BASE_URL=https://shiori-api-") {
		t.Fatalf("missing API endpoint: %s", calls)
	}
	if !strings.Contains(calls[deploy:], "--no-allow-unauthenticated --iap") || !strings.Contains(calls, "iap web set-iam-policy") {
		t.Fatalf("deploy must enable and configure IAP: %s", calls)
	}
}

func TestDeployCreatesMissingImageRepositoryBeforeBuild(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"scripts", "bin"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"common.sh", "deploy.sh"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "scripts", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "allow_accounts.yaml"), []byte("domains: [data-cloud.jp]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "go"), []byte("#!/usr/bin/env bash\nif [[ \"$3\" == --validate ]]; then exit 0; fi\nprintf '{\"bindings\":[{\"role\":\"roles/iap.httpsResourceAccessor\",\"members\":[\"domain:data-cloud.jp\"]}]}' > \"$5\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "calls")
	fake := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GCLOUD_LOG"
if [[ "$1 $2" == 'projects describe' ]]; then echo 123456789; fi
if [[ "$1 $2 $3" == 'artifacts repositories describe' ]]; then exit 1; fi
`
	if err := os.WriteFile(filepath.Join(root, "bin", "gcloud"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "deploy.sh"))
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "GCLOUD_LOG="+log,
		"PROJECT_ID=test-project", "SERVICE=shiori", "GOOGLE_CLIENT_ID=test-client")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(data)
	create := strings.Index(calls, "artifacts repositories create shiori-images")
	build := strings.Index(calls, "builds submit")
	if create < 0 || build < create {
		t.Fatalf("expected repository creation before build: %s", calls)
	}
}
