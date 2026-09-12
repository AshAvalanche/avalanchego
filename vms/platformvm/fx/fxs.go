// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fx

import (
	"errors"
	"reflect"
)

var ErrNoFx = errors.New("no feature extension available")

// Fxs is the ordered collection of feature extensions a chain runs, plus the
// table saying which one claims a given concrete type.
//
// Something has to exist for this, because Go dispatches on the receiver and
// never on the type of an argument: teaching the codec to decode an output into
// a particular concrete type settles what the *argument* is, not whose method
// runs. A single Fx instance therefore answers for every output, and rejects on
// its first type assertion the ones it does not recognize.
//
// The first extension of the collection is the default: every type no extension
// claims resolves to it.
type Fxs struct {
	fxs           []*Claim
	typeToFxIndex map[reflect.Type]int
}

// NewFxs builds the collection from [claims], in order, and fills the table
// from the types each one claims.
//
// The claim is data rather than a side effect, which is where this departs from
// the AVM. The AVM derives its table by observing the RegisterType calls each
// Fx makes while initializing, through a wrapping codec.Registry - it has no
// choice, since its Fx list comes from the chain's parameters and its codec is
// built at the same moment. The P-Chain has neither constraint: its list is
// compiled in, and its types are registered directly in txs.Codec.
//
// Stating the claim removes an order of calls that nothing enforces. With the
// side-effect mechanism, adding an extension without initializing it through
// the wrapped registry leaves the table empty: everything compiles, every
// existing test stays green, and the dispatch dispatches nothing.
func NewFxs(claims ...Claim) *Fxs {
	fxs := &Fxs{
		fxs:           make([]*Claim, len(claims)),
		typeToFxIndex: make(map[reflect.Type]int),
	}
	for i, claim := range claims {
		stored := claim
		fxs.fxs[i] = &stored
		for _, val := range claim.Types {
			fxs.typeToFxIndex[reflect.TypeOf(val)] = i
		}
	}
	return fxs
}

// All returns the claims of the collection, in order.
func (f *Fxs) All() []*Claim {
	if f == nil {
		return nil
	}
	return f.fxs
}

// Default returns the first extension of the collection, or nil if there is
// none.
func (f *Fxs) Default() Fx {
	if f == nil || len(f.fxs) == 0 {
		return nil
	}
	return f.fxs[0].Fx
}

// Get returns the extension claiming the concrete type of [val], the default
// when none does, or nil when there is no collection at all.
//
// Falling back rather than failing is what makes the collection a pure
// refactor: secp256k1fx.OutputOwners, which nobody lists, and every other
// unclaimed type keep resolving exactly where they used to.
//
// Returning nil on a nil receiver is deliberate too. A caller holding no
// collection - a Backend built by a test, say - must be able to fail at the
// call site rather than quietly get the default, which would produce the wrong
// output type at a place where the mistake only surfaces when the funds are
// spent.
func (f *Fxs) Get(val interface{}) Fx {
	if f == nil || len(f.fxs) == 0 {
		return nil
	}
	if index, ok := f.typeToFxIndex[reflect.TypeOf(val)]; ok {
		return f.fxs[index].Fx
	}
	return f.fxs[0].Fx
}

// VerifyTransfer resolves the extension from the consumed output and hands it
// the transaction-scoped context, when it is able to accept one.
//
// An extension that does not implement [ContextualFx] keeps being reached
// through the context-free method, so the collection changes nothing for it.
func (f *Fxs) VerifyTransfer(fxCtx *Context, tx, in, cred, utxo interface{}) error {
	resolved := f.Get(utxo)
	if resolved == nil {
		return ErrNoFx
	}
	if contextual, ok := resolved.(ContextualFx); ok {
		return contextual.VerifyTransferWithContext(fxCtx, tx, in, cred, utxo)
	}
	return resolved.VerifyTransfer(tx, in, cred, utxo)
}

// VerifyPermission resolves the extension from the control group and hands it
// the transaction-scoped context, when it is able to accept one.
//
// Callers with no authorization to offer must keep calling [Fx.VerifyPermission]
// on a single Fx instead. That is what confines an extension to the control
// groups it can actually prove assent for - subnet authorization goes through
// the context-free method, so a warp owner named as a subnet's control group is
// refused structurally, with no rule to write or remember.
func (f *Fxs) VerifyPermission(fxCtx *Context, tx, in, cred, controlGroup interface{}) error {
	resolved := f.Get(controlGroup)
	if resolved == nil {
		return ErrNoFx
	}
	if contextual, ok := resolved.(ContextualFx); ok {
		return contextual.VerifyPermissionWithContext(fxCtx, tx, in, cred, controlGroup)
	}
	return resolved.VerifyPermission(tx, in, cred, controlGroup)
}
