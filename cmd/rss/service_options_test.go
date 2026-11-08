package main

import (
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestServiceTLSIntentAndEffectiveConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flag         string
		configured   *bool
		wantEnabled  bool
		wantExplicit bool
	}{
		{name: "default", wantEnabled: true},
		{name: "configured_off", configured: boolPointer(false), wantEnabled: false},
		{name: "configured_on", configured: boolPointer(true), wantEnabled: true},
		{name: "explicit_on_overrides_config", flag: "true", configured: boolPointer(false), wantEnabled: true, wantExplicit: true},
		{name: "explicit_off_overrides_config", flag: "false", configured: boolPointer(true), wantEnabled: false, wantExplicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousTLS := svcTLS
			defer func() { svcTLS = previousTLS }()
			cmd := &cobra.Command{Use: "install"}
			cmd.Flags().BoolVar(&svcTLS, "tls", true, "TLS")
			if tc.flag != "" {
				require.NoError(t, cmd.Flags().Set("tls", tc.flag))
			}
			cfg := config.TestConfig()
			cfg.SourcePath = "/absolute/service.toml"
			cfg.Web.TLS.Enabled = tc.configured
			options := serviceInstallOptions(cmd, cfg, "/absolute/fwrd", "/absolute/fwrd.db")
			require.Equal(t, tc.wantExplicit, options.TLSExplicit)
			require.Equal(t, svcTLS, options.TLS)
			require.Equal(t, cfg.SourcePath, options.Config)
			require.Equal(t, tc.wantEnabled, resolveTLSOption(cmd, cfg, svcTLS))
		})
	}
}

func boolPointer(value bool) *bool { return &value }

func TestServiceInstallOptionsSnapshotInterfaceAddresses(t *testing.T) {
	previous := svcMDNSIPs
	defer func() { svcMDNSIPs = previous }()
	svcMDNSIPs = []string{"192.0.2.1"}
	options := serviceInstallOptions(nil, nil, "/absolute/fwrd", "")
	svcMDNSIPs[0] = "192.0.2.2"
	require.Equal(t, []string{"192.0.2.1"}, options.MDNSIPs)
}
