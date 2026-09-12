// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
)

var ErrRewardsNotSorted = errors.New("rewards are not sorted by increasing output index")

// Reward names one reward UTXO of an attestation.
//
// The amount is not there for accounting. Spending a UTXO takes a
// TransferableInput whose Amt equals the output's exactly, so an aggregate sum
// would describe a balance its owner could not spend.
type Reward struct {
	OutputIndex uint32 `serialize:"true" json:"outputIndex"`
	Amount      uint64 `serialize:"true" json:"amount"`
}

// StakeSettled attests that a staking transaction has settled, and names the
// reward UTXOs it produced for one owner.
//
// A staking reward is predictable neither in amount nor in position. Whether it
// is paid at all depends on the outcome of the RewardValidatorTx, itself a
// function of measured uptime; its amount is the PotentialReward the P-chain
// computes; and - less obviously - its OutputIndex depends on that same
// outcome, a delegator's reward sitting at a different rank depending on
// whether the validation reward was paid. No commitment made in advance can
// contain that.
//
// Nothing here is emitted or stored: the message is derived from accepted state
// on request, so retention and pruning simply do not arise.
type StakeSettled struct {
	payload

	// StakingTxID is the staking transaction that settled. Every reward UTXO
	// below carries it as its TxID, which is why the entries hold only an
	// index.
	StakingTxID ids.ID `serialize:"true" json:"stakingTxID"`

	// SourceChainID and SourceAddress are the warp owner this attestation is
	// about, named by the pair rather than by a hash of it.
	//
	// Twenty bytes more, and one codec reproduction less on the Solidity side:
	// a contract checks that an attestation concerns it with
	// sourceAddress == address(this), instead of reimplementing a Go
	// linearcodec in another language to recompute a hash with no other use.
	// It is also exactly the pair a UTXO's warpfx.Owner carries, so the
	// verifier compares structures rather than digests.
	SourceChainID ids.ID      `serialize:"true" json:"sourceChainID"`
	SourceAddress ids.ShortID `serialize:"true" json:"sourceAddress"`

	// Rewards are this owner's reward UTXOs, sorted by increasing OutputIndex.
	//
	// An empty list is a legitimate value - "settled with no reward" - and is
	// never confused with "not settled yet", since the message is only signed
	// once the staker has left the set.
	Rewards []Reward `serialize:"true" json:"rewards"`
}

// Verify checks that the rewards are sorted by strictly increasing output
// index.
//
// The ordering is not cosmetic: it is what lets a quorum exist at all.
// GetRewardUTXOs returns a slice whose order comes from the database, and BLS
// aggregation requires every validator to sign *identical* bytes. Without a
// normative order two nodes produce two different messages and no quorum ever
// forms - a silent failure, where everything compiles, every node answers
// correctly, and the relayer simply never reaches 67%.
func (s *StakeSettled) Verify() error {
	return verifyRewardsSorted(s.Rewards)
}

// NewStakeSettled creates a new initialized StakeSettled.
func NewStakeSettled(
	stakingTxID ids.ID,
	sourceChainID ids.ID,
	sourceAddress ids.ShortID,
	rewards []Reward,
) (*StakeSettled, error) {
	msg := &StakeSettled{
		StakingTxID:   stakingTxID,
		SourceChainID: sourceChainID,
		SourceAddress: sourceAddress,
		Rewards:       rewards,
	}
	return msg, Initialize(msg)
}

// ParseStakeSettled parses bytes into an initialized StakeSettled.
func ParseStakeSettled(b []byte) (*StakeSettled, error) {
	payloadIntf, err := Parse(b)
	if err != nil {
		return nil, err
	}
	payload, ok := payloadIntf.(*StakeSettled)
	if !ok {
		return nil, fmt.Errorf("%w: %T", ErrWrongType, payloadIntf)
	}
	return payload, nil
}

func verifyRewardsSorted(rewards []Reward) error {
	for i := 1; i < len(rewards); i++ {
		if rewards[i-1].OutputIndex >= rewards[i].OutputIndex {
			return fmt.Errorf(
				"%w: index %d follows %d",
				ErrRewardsNotSorted,
				rewards[i].OutputIndex,
				rewards[i-1].OutputIndex,
			)
		}
	}
	return nil
}
