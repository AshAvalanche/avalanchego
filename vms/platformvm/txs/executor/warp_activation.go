// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/vms/platformvm/platform"
	"github.com/ava-labs/avalanchego/vms/platformvm/stakeable"
	"github.com/ava-labs/avalanchego/vms/warpfx"
)

var (
	ErrWarpUTXOsNotActivated = errors.New("warp UTXOs are not activated")
	ErrWarpOutputNotLockable = errors.New("warp output cannot be stakeable locked")

	warpfxPackage = reflect.TypeOf(warpfx.Owner{}).PkgPath()
	lockOutType   = reflect.TypeOf((*stakeable.LockOut)(nil))
)

// VerifyWarpUTXOsActivated refuses a transaction that so much as *mentions* a
// warpfx type before the upgrade that introduces them.
//
// What it protects against is a fork, not a feature used too early. Codec
// registration is unconditional - gating belongs to verification, never to
// decoding - so a node on an older binary cannot decode types 43/44/45 at all,
// nor the block carrying them. It would not reject the transaction; it would be
// unable to process the chain. Hence a rule about mentioning rather than
// spending: receiving creates an undecodable type just as surely.
//
// The traversal is reflective because a hand-written enumeration of Outs,
// StakeOuts, the rewards owners and Creds would be wrong the first time a field
// is added, and wrong here means a fork. It costs nothing once the upgrade is
// active: this then returns on its first line, forever.
func VerifyWarpUTXOsActivated(
	upgrades upgrade.Config,
	chainTime time.Time,
	tx *platform.Tx,
) error {
	if upgrades.IsHeliconActivated(chainTime) {
		return nil
	}

	return walkTx(tx, func(value reflect.Value) error {
		if !isWarpType(value.Type()) {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrWarpUTXOsNotActivated, value.Type())
	})
}

// verifyWarpOutputsNotLocked refuses a warpfx output wrapped in a
// stakeable.LockOut.
//
// warpfx.TransferOutput deliberately carries no Locktime: the UTXO verifier
// evaluates locktimes against the node's local clock, while everything else in
// warpfx is evaluated against the chain time, so nobody can enforce one end to
// end. The behaviour returns through a side door, LockOut wrapping an interface
// and refusing only nested LockOuts.
//
// A third party can build it: producing locked funds out of unlocked ones is
// allowed, and receiving needs no authorization, so an ordinary signed BaseTx
// can lock funds in favour of somebody else's warp owner. The owner gets them
// back at maturity - not theft, but a binding gift on a time base this design
// rejected, and somebody else's doing, which is what makes it a rule.
//
// Unlike the activation guard, this one runs forever.
func verifyWarpOutputsNotLocked(tx *platform.Tx) error {
	return walkTx(tx, func(value reflect.Value) error {
		if value.Type() != lockOutType || value.IsNil() {
			return nil
		}

		// Read the wrapped output by reflection rather than asserting: the
		// value may sit in an unexported field, where Interface() panics.
		inner := value.Elem().FieldByName("TransferableOut")
		if inner.Kind() != reflect.Interface || inner.IsNil() {
			return nil
		}
		if !isWarpType(inner.Elem().Type()) {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrWarpOutputNotLockable, inner.Elem().Type())
	})
}

// isWarpType reports whether [t], with any pointers stripped, is declared by
// the warpfx package.
//
// Matching on the package rather than on a list of types is what keeps this
// honest: a type added to warpfx is covered the day it is added.
func isWarpType(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() == warpfxPackage
}

// walkTx calls [fn] on every value reachable from [tx], stopping at the first
// error it returns.
//
// A decoded transaction is a tree - the codec builds fresh values and cannot
// produce a cycle - so this needs no visited set.
func walkTx(tx *platform.Tx, fn func(reflect.Value) error) error {
	return walkValue(reflect.ValueOf(tx), fn)
}

func walkValue(value reflect.Value, fn func(reflect.Value) error) error {
	if !value.IsValid() {
		return nil
	}
	if err := fn(value); err != nil {
		return err
	}

	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return walkValue(value.Elem(), fn)

	case reflect.Struct:
		for i := range value.NumField() {
			if err := walkValue(value.Field(i), fn); err != nil {
				return err
			}
		}

	case reflect.Slice, reflect.Array:
		// Byte slices and arrays are the overwhelming majority of the leaves
		// here - every ids.ID is one - and none of them can hold a warpfx type.
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		for i := range value.Len() {
			if err := walkValue(value.Index(i), fn); err != nil {
				return err
			}
		}

	case reflect.Map:
		for _, key := range value.MapKeys() {
			if err := walkValue(value.MapIndex(key), fn); err != nil {
				return err
			}
		}
	}
	return nil
}
