package service

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
)

// UpdateService reports the running binary's own version and the current
// security-relevant config posture. This fork removes upstream's binary
// self-update/rollback feature (which downloaded and swapped the running
// binary from GitHub Releases): this fork is build-from-source only, and
// updates happen via `git pull` + rebuild, never by downloading a prebuilt
// binary at runtime. See FORK_MAINTENANCE.md for the full rationale and the
// update procedure.
type UpdateService struct {
	currentVersion string
	cfg            *config.Config
}

// NewUpdateService creates a new UpdateService.
func NewUpdateService(version string, cfg *config.Config) *UpdateService {
	return &UpdateService{currentVersion: version, cfg: cfg}
}

// CurrentVersion returns the running binary's version string.
func (s *UpdateService) CurrentVersion() string {
	return s.currentVersion
}

// SecurityWarnings returns a plain-language warning for each insecure
// security.url_allowlist setting currently in effect, empty when the
// instance is running with the secure defaults. Surfaced in the admin UI so
// an insecure config is never silent.
func (s *UpdateService) SecurityWarnings() []string {
	if s.cfg == nil {
		return nil
	}
	allowlist := s.cfg.Security.URLAllowlist
	var warnings []string
	if !allowlist.Enabled {
		warnings = append(warnings, "security.url_allowlist.enabled is false: upstream/pricing/CRS URLs are not restricted to an allowlist.")
	}
	if allowlist.AllowPrivateHosts {
		warnings = append(warnings, "security.url_allowlist.allow_private_hosts is true: requests to localhost/private-network addresses are allowed (SSRF risk).")
	}
	if allowlist.AllowInsecureHTTP {
		warnings = append(warnings, "security.url_allowlist.allow_insecure_http is true: plain HTTP upstream URLs are allowed (credentials/tokens can be sent unencrypted).")
	}
	return warnings
}
