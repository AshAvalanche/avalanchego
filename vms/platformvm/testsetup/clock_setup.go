// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package testsetup

import (
	"time"

	"github.com/ava-labs/avalanchego/utils/timer/mockable"
)

// useDefault is just an attempt to set clock time the way it is currently
// in different packages. TODO drop it and find common, meaningfull times
func DefaultClock(fork ActiveFork, useDefault bool) *mockable.Clock {
	now := GenesisTime
	if !useDefault && (fork == CortinaFork || fork == DFork) {
		// 1 second after Banff fork
		now = ValidateEndTime.Add(-2 * time.Second)
	}
	clk := &mockable.Clock{}
	clk.Set(now)
	return clk
}
