package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := LoadFrom(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8090" || c.APIKey != "sim_test_key" || c.Seed != 42 || c.QuoteTTL != 15*time.Second ||
		c.FeeBps != 50 || len(c.Symbols) != 5 || !c.AutoFill || c.FillProb != 0.35 {
		t.Errorf("defaults = %+v", c)
	}
}

func TestOverridesAndErrors(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"SIM_SEED": "7", "SIM_QUOTE_TTL": "3s", "SIM_SYMBOLS": "X:5"}))
	if err != nil || c.Seed != 7 || c.QuoteTTL != 3*time.Second || len(c.Symbols) != 1 {
		t.Errorf("overrides: %+v %v", c, err)
	}
	for _, bad := range []map[string]string{
		{"SIM_SEED": "abc"},
		{"SIM_QUOTE_TTL": "soon"},
		{"SIM_FILL_PROB": "1.5"},
		{"SIM_SYMBOLS": "AAPL"},
		{"SIM_CANCEL_DELAY_MIN": "5s", "SIM_CANCEL_DELAY_MAX": "1s"},
	} {
		if _, err := LoadFrom(env(bad)); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestContainerSettings(t *testing.T) {
	c, err := LoadFrom(env(nil))
	if err != nil || !c.AutoResolveCancels || len(c.DevTrustedNets) != 0 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = LoadFrom(env(map[string]string{
		"SIM_AUTO_RESOLVE_CANCELS": "false",
		"SIM_DEV_TRUSTED_CIDRS":    " 172.28.0.0/16 , 10.1.0.0/24",
	}))
	if err != nil || c.AutoResolveCancels || len(c.DevTrustedNets) != 2 || c.DevTrustedNets[0].String() != "172.28.0.0/16" {
		t.Fatalf("overrides: %+v %v", c, err)
	}
	if _, err := LoadFrom(env(map[string]string{"SIM_DEV_TRUSTED_CIDRS": "not-a-cidr"})); err == nil {
		t.Fatal("a bad CIDR should fail")
	}
}
