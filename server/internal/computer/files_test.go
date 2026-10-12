package computer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderFiles(t *testing.T) {
	got, err := RenderFiles("http://10.10.0.31:3000", "Zhang", "zhang@company.example", "http://10.12.0.79", "glpat-x", "ANTHROPIC_API_KEY=sk", "mul_pat")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Gitconfig, `name = "Zhang"`) || strings.Contains(got.Gitconfig, "glpat-x") {
		t.Fatalf("gitconfig leaked or missing name: %s", got.Gitconfig)
	}
	if !strings.Contains(got.Gitconfig, "gitlab.token") {
		t.Fatal("git helper must read the token file")
	}
	if !strings.Contains(got.Gitconfig, `helper = "!f()`) {
		t.Fatalf("helper must be a quoted gitconfig value: %s", got.Gitconfig)
	}
	if !strings.Contains(got.MulticaConfig, `"token":"mul_pat"`) && !strings.Contains(got.MulticaConfig, `"token": "mul_pat"`) {
		t.Fatalf("config: %s", got.MulticaConfig)
	}
	if strings.Contains(DaemonUnit, "mul_pat") || strings.Contains(DaemonUnit, "glpat") {
		t.Fatal("unit must not embed secrets")
	}
}

func TestGitCredentialFill(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	tokenPath := filepath.Join(home, ".config", "multica-provision", "gitlab.token")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("glpat-real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := RenderFiles("http://10.10.0.31:3000", "Zhang", "zhang@company.example", "http://10.12.0.79", "glpat-real", "", "mul_pat")
	if err != nil {
		t.Fatal(err)
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitconfig, []byte(files.Gitconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "credential", "fill")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_GLOBAL="+gitconfig)
	cmd.Stdin = strings.NewReader("protocol=http\nhost=10.12.0.79\n\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git credential fill: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "username=oauth2") || !strings.Contains(text, "password=glpat-real") {
		t.Fatalf("credential fill output:\n%s", text)
	}
	other := exec.Command("git", "credential", "fill")
	other.Dir = home
	other.Env = cmd.Env
	other.Stdin = strings.NewReader("protocol=https\nhost=example.com\n\n")
	otherOut, _ := other.CombinedOutput()
	if strings.Contains(string(otherOut), "glpat-real") || strings.Contains(string(otherOut), "password=") {
		t.Fatalf("other host received gitlab token:\n%s", otherOut)
	}
}

func TestRejectsNUL(t *testing.T) {
	if _, err := RenderFiles("http://10.10.0.31:3000", "Zhang\x00", "zhang@company.example", "http://10.12.0.79", "glpat-x", "", "mul_pat"); err == nil {
		t.Fatal("accepted NUL in git name")
	}
	if _, err := RenderFiles("http://10.10.0.31:3000", "Zhang", "zhang@company.example", "http://10.12.0.79", "gl\x00pat", "", "mul_pat"); err == nil {
		t.Fatal("accepted NUL in gitlab token")
	}
	if _, err := ValidateModelEnv("ANTHROPIC_API_KEY=sk\x00"); err == nil {
		t.Fatal("accepted NUL in model env")
	}
}

func TestModelEnvRejectsDaemonOverrides(t *testing.T) {
	if _, err := ValidateModelEnv("MULTICA_TOKEN=nope"); err == nil {
		t.Fatal("accepted MULTICA_TOKEN")
	}
	if _, err := ValidateModelEnv("PATH=/tmp"); err == nil {
		t.Fatal("accepted PATH")
	}
}
