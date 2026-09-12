// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
)

// CycleSettled attests that the cycle closed by rewardTxID produced, for one
// owner, the reward UTXOs it names.
//
// It is separate from StakeSettled because the two staking families do not
// index rewards under the same key - a property of the existing code, not a
// choice here:
//
//   - permissionless staking indexes them under the *staking* txID, once;
//   - an auto-renewed validator, under the RewardAutoRenewedValidatorTx txID,
//     once per cycle.
//
// GetRewardUTXOs(stakingTxID) is therefore always empty for an auto-renewed
// validator, and a StakeSettled signed after it left would announce zero
// rewards to an owner paid every cycle: true to the message's definition, and
// thoroughly misleading.
type CycleSettled struct {
	payload

	// RewardTxID is the RewardAutoRenewedValidatorTx that closed the cycle.
	//
	// The staking txID is deliberately absent: GetTx(rewardTxID) returns the
	// reward transaction, whose TxID field *is* the staking txID. Restating a
	// derivable value in a signed message is two sources of truth nothing
	// forces to agree.
	RewardTxID ids.ID `serialize:"true" json:"rewardTxID"`

	// SourceChainID and SourceAddress are the warp owner this attestation is
	// about. See StakeSettled for why the pair rather than a hash.
	SourceChainID ids.ID      `serialize:"true" json:"sourceChainID"`
	SourceAddress ids.ShortID `serialize:"true" json:"sourceAddress"`

	// Rewards are this owner's reward UTXOs for that cycle, sorted by
	// increasing OutputIndex.
	Rewards []Reward `serialize:"true" json:"rewards"`
}

// Verify checks the same ordering StakeSettled does, and for the same reason:
// without identical bytes, no quorum forms.
func (c *CycleSettled) Verify() error {
	return verifyRewardsSorted(c.Rewards)
}

// NewCycleSettled creates a new initialized CycleSettled.
func NewCycleSettled(
	rewardTxID ids.ID,
	sourceChainID ids.ID,
	sourceAddress ids.ShortID,
	rewards []Reward,
) (*CycleSettled, error) {
	msg := &CycleSettled{
		RewardTxID:    rewardTxID,
		SourceChainID: sourceChainID,
		SourceAddress: sourceAddress,
		Rewards:       rewards,
	}
	return msg, Initialize(msg)
}

// ParseCycleSettled parses bytes into an initialized CycleSettled.
func ParseCycleSettled(b []byte) (*CycleSettled, error) {
	payloadIntf, err := Parse(b)
	if err != nil {
		return nil, err
	}
	payload, ok := payloadIntf.(*CycleSettled)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrWrongType, payloadIntf)
	}
	return payload, nil
}
