package main

import (
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/service"
	"github.com/spf13/cobra"
)

// resolveTLSOption shares flag/config precedence between serve and service
// installation diagnostics. An omitted CLI flag must not override configuration.
func resolveTLSOption(cmd *cobra.Command, cfg *config.Config, flagValue bool) bool {
	if cmd != nil && cmd.Flags().Changed("tls") {
		return flagValue
	}
	if cfg != nil && cfg.Web.TLS.Enabled != nil {
		return *cfg.Web.TLS.Enabled
	}
	return true
}

// serviceInstallOptions preserves explicit flag intent when the service later
// launches a fresh process, rather than treating true as an omitted default.
func serviceInstallOptions(cmd *cobra.Command, cfg *config.Config, bin, database string) *service.Options {
	configPath := ""
	if cfg != nil {
		configPath = cfg.SourcePath
	}
	return &service.Options{
		BinPath:              bin,
		Addr:                 svcAddr,
		MDNS:                 svcMDNS,
		MDNSName:             svcMDNSName,
		MDNSIPs:              append([]string(nil), svcMDNSIPs...),
		MDNSIface:            svcMDNSIface,
		TLS:                  svcTLS,
		TLSExplicit:          cmd != nil && cmd.Flags().Changed("tls"),
		TLSMode:              svcTLSMode,
		TLSCert:              svcTLSCert,
		TLSKey:               svcTLSKey,
		Config:               configPath,
		DB:                   database,
		AllowUnauthenticated: svcAllowUnauthenticated,
	}
}
