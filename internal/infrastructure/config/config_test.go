package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
