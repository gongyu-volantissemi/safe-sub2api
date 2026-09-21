//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestUpdateServiceCurrentVersion(t *testing.T) {
	svc := NewUpdateService("0.1.147", &config.Config{})

	require.Equal(t, "0.1.147", svc.CurrentVersion())
}

func TestUpdateServiceSecurityWarningsNilConfig(t *testing.T) {
	svc := NewUpdateService("0.1.147", nil)

	require.Empty(t, svc.SecurityWarnings())
}

func TestUpdateServiceSecurityWarningsSecureDefaults(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.AllowPrivateHosts = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = false

	svc := NewUpdateService("0.1.147", cfg)

	require.Empty(t, svc.SecurityWarnings())
}

func TestUpdateServiceSecurityWarningsFlagsEachInsecureSetting(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	svc := NewUpdateService("0.1.147", cfg)

	warnings := svc.SecurityWarnings()
	require.Len(t, warnings, 3)
	require.Contains(t, warnings[0], "url_allowlist.enabled")
	require.Contains(t, warnings[1], "allow_private_hosts")
	require.Contains(t, warnings[2], "allow_insecure_http")
}
