// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package nativeexport

import (
	"fmt"

	"github.com/ava-labs/avalanchego/graft/coreth/precompile/contract"
	"github.com/ava-labs/avalanchego/graft/coreth/precompile/modules"
	"github.com/ava-labs/avalanchego/graft/coreth/precompile/precompileconfig"
)

var _ contract.Configurator = (*configurator)(nil)

// ConfigKey names this precompile in an upgrade's JSON. It must be unique
// across every registered precompile.
const ConfigKey = "nativeExportConfig"

// Module registers the precompile.
//
// modules.RegisterModule is a global registry filled by init(), and
// graft/coreth/precompile/registry does nothing but blank-import packages to
// trigger those. saevm's genesis.go does not go through that registry: it
// references the Warp precompile package directly. A package living under
// vms/saevm and imported by genesis.go therefore registers the same way,
// without coreth having to know it exists.
//
// ⚠️ Registering makes the ConfigKey *parsable* everywhere, coreth included.
// Activation is separate, and passes only through PrecompileUpgrades, which
// only saevm's genesis.go fills.
var Module = modules.Module{
	ConfigKey:    ConfigKey,
	Address:      ContractAddress,
	Contract:     ExportPrecompile,
	Configurator: &configurator{},
}

type configurator struct{}

func init() {
	if err := modules.RegisterModule(Module); err != nil {
		panic(err)
	}
}

// MakeConfig returns an empty config for the codec to fill.
func (*configurator) MakeConfig() precompileconfig.Config {
	return new(Config)
}

// Configure stores nothing: the counter slot is written lazily, on the first
// export of each transaction.
func (*configurator) Configure(
	_ precompileconfig.ChainConfig,
	cfg precompileconfig.Config,
	_ contract.StateDB,
	_ contract.ConfigurationBlockContext,
) error {
	if _, ok := cfg.(*Config); !ok {
		return fmt.Errorf("expected config type %T, got %T: %v", &Config{}, cfg, cfg)
	}
	return nil
}
