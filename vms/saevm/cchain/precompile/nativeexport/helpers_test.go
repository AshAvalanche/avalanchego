// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package nativeexport

import (
	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/libevm/stateconf"
)

// fakeStateDB is the slice of contract.StateDB the counter needs. Using the
// generated mock would mean scripting every Get/Set in order, which tests the
// script rather than the counter.
type fakeStateDB struct {
	txHash common.Hash
	state  map[common.Hash]common.Hash
}

func newFakeStateDB() *fakeStateDB {
	return &fakeStateDB{state: make(map[common.Hash]common.Hash)}
}

func (f *fakeStateDB) TxHash() common.Hash { return f.txHash }

func (f *fakeStateDB) GetState(_ common.Address, slot common.Hash, _ ...stateconf.StateDBStateOption) common.Hash {
	return f.state[slot]
}

func (f *fakeStateDB) SetState(_ common.Address, slot, value common.Hash, _ ...stateconf.StateDBStateOption) {
	f.state[slot] = value
}
