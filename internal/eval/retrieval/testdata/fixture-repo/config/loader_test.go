package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	env := map[string]string{
		"FIXTURE_STORE":     "file",
		"FIXTURE_TOKEN_TTL": "30m",
	}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StoreKind != "file" || cfg.TokenTTL != 30*time.Minute {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.Addr != DefaultConfig().Addr {
		t.Errorf("unset variable must keep the default, got %q", cfg.Addr)
	}
	env["FIXTURE_TOKEN_TTL"] = "soon"
	if _, err := Load(func(k string) string { return env[k] }); err == nil {
		t.Error("unparseable TTL must be an error")
	}
}
