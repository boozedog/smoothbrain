package config

import (
	"os"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_Minimal(t *testing.T) {
	path := writeConfig(t, `{"http":{"address":"localhost:8080"},"database":"test.db"}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTP.Address != "localhost:8080" {
		t.Errorf("HTTP.Address = %q, want %q", cfg.HTTP.Address, "localhost:8080")
	}
	if cfg.Database != "test.db" {
		t.Errorf("Database = %q, want %q", cfg.Database, "test.db")
	}
}

func TestLoad_Defaults(t *testing.T) {
	path := writeConfig(t, `{}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTP.Address != "127.0.0.1:8080" {
		t.Errorf("HTTP.Address = %q, want %q", cfg.HTTP.Address, "127.0.0.1:8080")
	}
	if cfg.Database != "smoothbrain.db" {
		t.Errorf("Database = %q, want %q", cfg.Database, "smoothbrain.db")
	}
}

func TestLoad_EnvExpansion(t *testing.T) {
	t.Setenv("SMOOTHBRAIN_TEST_ADDR", "0.0.0.0:9999")
	path := writeConfig(t, `{"http":{"address":"$SMOOTHBRAIN_TEST_ADDR"},"database":"test.db"}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTP.Address != "0.0.0.0:9999" {
		t.Errorf("HTTP.Address = %q, want %q", cfg.HTTP.Address, "0.0.0.0:9999")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("Load() expected error for nonexistent file, got nil")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	path := writeConfig(t, `{not valid json}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid JSON, got nil")
	}
}

func TestLoad_EmptyHTTPAddress(t *testing.T) {
	path := writeConfig(t, `{"http":{"address":""},"database":"x.db"}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected validation error for empty http.address, got nil")
	}
	if !strings.Contains(err.Error(), "http.address") {
		t.Errorf("error = %q, want it to mention http.address", err)
	}
}

func TestLoad_EmptyDatabase(t *testing.T) {
	path := writeConfig(t, `{"http":{"address":"localhost:8080"},"database":""}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected validation error for empty database, got nil")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Errorf("error = %q, want it to mention database", err)
	}
}

func TestLoad_TailscaleValidation(t *testing.T) {
	path := writeConfig(t, `{"tailscale":{"enabled":true,"service_name":""}}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected validation error for empty tailscale.service_name, got nil")
	}
	if !strings.Contains(err.Error(), "service_name") {
		t.Errorf("error = %q, want it to mention service_name", err)
	}
}

func TestLoad_RouteValidation_EmptyName(t *testing.T) {
	path := writeConfig(t, `{"routes":[{"name":"","source":"x","sink":{"plugin":"y"}}]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected validation error for empty route name, got nil")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, want it to mention name", err)
	}
}

func TestLoad_RouteValidation_DuplicateName(t *testing.T) {
	path := writeConfig(t, `{"routes":[
		{"name":"r1","source":"a","sink":{"plugin":"b"}},
		{"name":"r1","source":"c","sink":{"plugin":"d"}}
	]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected validation error for duplicate route name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want it to mention duplicate", err)
	}
}

func TestLoad_RouteValidation_EmptySource(t *testing.T) {
	path := writeConfig(t, `{"routes":[{"name":"r1","source":"","sink":{"plugin":"y"}}]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected validation error for empty route source, got nil")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("error = %q, want it to mention source", err)
	}
}
