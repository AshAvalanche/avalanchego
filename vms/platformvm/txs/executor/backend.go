// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/uptime"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/vms/platformvm/config"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/utxo"
)

type Backend struct {
	Config *config.Internal
	Ctx    *snow.Context
	Clk    *mockable.Clock

	// Fx is the default feature extension, and it is not redundant with Fxs.
	// Subnet authorization resolves nothing - a control group is secp256k1 by
	// construction - so subnet_tx_verification.go keeps taking a single Fx.
	Fx fx.Fx

	// Fxs is the collection every spending path dispatches through.
	Fxs *fx.Fxs

	FlowChecker  utxo.Verifier
	Uptimes      uptime.Calculator
	Bootstrapped *utils.Atomic[bool]
}
