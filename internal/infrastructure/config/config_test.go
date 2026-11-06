package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadPublicURL(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"", true},
		{"https://example.test/k8sctl", true},
		{"http://127.0.0.1:8091", true},
		{"http://example.test/k8sctl", false},
		{"https://user:password@example.test", false},
		{"https://example.test?token=value", false},
		{"https://example.test#fragment", false},
		{"/k8sctl", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("SERVER_PUBLIC_URL", "")
			dir := t.TempDir()
			body := "server:\n  host: 127.0.0.1\n  port: 8091\n  public-url: " + strconv.Quote(tc.value) + "\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err == nil && cfg.Server.PublicURL != tc.value {
				t.Fatal("download public URL was not loaded")
			}
		})
	}
}

func TestControlAPIKeyFile(t *testing.T) {
	for _, tc := range []struct {
		name, contents, directKey, wantError string
		missing                              bool
	}{
		{name: "mounted secret", contents: "test-mounted-key\n"},
		{name: "missing secret", missing: true, wantError: "read control API key file failed"},
		{name: "empty secret", contents: "\n", wantError: "control API key file is empty"},
		{name: "conflicting sources", contents: "test-mounted-key", directKey: "test-other-key", wantError: "configure only one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CONTROL_API_KEY", "")
			t.Setenv("CONTROL_API_KEY_FILE", "")
			t.Setenv("SERVER_HOST", "")
			t.Setenv("SERVER_PORT", "")
			dir := t.TempDir()
			keyPath := filepath.Join(dir, "api-key")
			if !tc.missing {
				if err := os.WriteFile(keyPath, []byte(tc.contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			body := "server:\n  host: 0.0.0.0\n  port: 8091\ncontrol:\n  api-key: " + strconv.Quote(tc.directKey) + "\n  api-key-file: " + strconv.Quote(keyPath) + "\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q", tc.wantError)
				}
				if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), "test-mounted-key") {
					t.Fatal("error exposes key details")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Control.APIKey != "test-mounted-key" {
				t.Fatal("mounted key was not loaded or trailing newline was retained")
			}
		})
	}
}

func TestAuthenticationConfigEnvironment(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  host: 0.0.0.0\n  port: 8091\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("AUTH_COMPANY_ENABLED", "true")
	t.Setenv("AUTH_COMPANY_ISSUER", "company")
	t.Setenv("AUTH_COMPANY_AUDIENCE", "k8s-ctl")
	t.Setenv("AUTH_COMPANY_KEY_ID", "company-v1")
	t.Setenv("AUTH_COMPANY_PUBLIC_KEY_FILE", "/mounted/company.pem")
	t.Setenv("AUTH_PAT_ENABLED", "true")
	t.Setenv("AUTH_PAT_DATABASE_PATH", "/data/auth.db")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.Enabled || !cfg.Auth.Company.Enabled || cfg.Auth.Company.Issuer != "company" || cfg.Auth.Company.Audience != "k8s-ctl" || cfg.Auth.Company.KeyID != "company-v1" || cfg.Auth.Company.PublicKeyFile != "/mounted/company.pem" || !cfg.Auth.PAT.Enabled || cfg.Auth.PAT.DatabasePath != "/data/auth.db" {
		t.Fatal("authentication environment configuration not loaded")
	}
}

func TestJWTModeDoesNotRequireLegacyControlSecret(t *testing.T) {
	dir := t.TempDir()
	body := "server:\n  host: 0.0.0.0\n  port: 8091\nauth:\n  enabled: true\ncontrol:\n  api-key-file: " + strconv.Quote(filepath.Join(dir, "missing-api-key")) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.Enabled {
		t.Fatal("authentication must remain enabled")
	}
	// Disabling authentication must restore the legacy mandatory credential check.
	body = strings.Replace(body, "enabled: true", "enabled: false", 1)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(dir); err == nil || !strings.Contains(err.Error(), "read control API key file failed") {
		t.Fatal("legacy mode must still require its Secret")
	}
}

func TestSystemShellDoesNotShadowTerminalDefaults(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  host: 127.0.0.1\n  port: 8091\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shell.Terminal.IdleSeconds != 300 || cfg.Shell.Terminal.MaxSessions != 16 {
		t.Fatal("terminal defaults shadowed by system SHELL")
	}
	t.Setenv("K8S_CTL_SHELL_TERMINAL_MAX_SESSIONS", "8")
	cfg, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shell.Terminal.MaxSessions != 8 {
		t.Fatal("explicit terminal environment override not applied")
	}
}
