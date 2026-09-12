// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package nativeexport

import "github.com/ava-labs/avalanchego/graft/coreth/precompile/precompileconfig"

var _ precompileconfig.Config = (*Config)(nil)

// Config activates the export precompile. It carries no parameter: the only
// thing to configure is when it turns on.
type Config struct {
	precompileconfig.Upgrade
}

// NewConfig enables the precompile at [blockTimestamp].
func NewConfig(blockTimestamp *uint64) *Config {
	return &Config{Upgrade: precompileconfig.Upgrade{BlockTimestamp: blockTimestamp}}
}

// NewDisableConfig disables it at [blockTimestamp].
func NewDisableConfig(blockTimestamp *uint64) *Config {
	return &Config{Upgrade: precompileconfig.Upgrade{
		BlockTimestamp: blockTimestamp,
		Disable:        true,
	}}
}

func (*Config) Key() string { return ConfigKey }

func (c *Config) Equal(cfg precompileconfig.Config) bool {
	other, ok := cfg.(*Config)
	return ok && c.Upgrade.Equal(&other.Upgrade)
}

func (*Config) Verify(precompileconfig.ChainConfig) error { return nil }
