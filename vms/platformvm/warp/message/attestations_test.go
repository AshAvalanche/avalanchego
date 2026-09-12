// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
)

func TestTxExecuted(t *testing.T) {
	require := require.New(t)

	msg, err := NewTxExecuted(ids.GenerateTestID(), ids.GenerateTestID())
	require.NoError(err)

	parsed, err := ParseTxExecuted(msg.Bytes())
	require.NoError(err)
	require.Equal(msg, parsed)
}

// TestTxExecutedBytes pins the wire format:
//
//	+---------------+------------+----------+
//	|       codecID :     uint16 |  2 bytes |
//	|        typeID :     uint32 |  4 bytes |
//	|          txID :   [32]byte | 32 bytes |
//	|      authHash :   [32]byte | 32 bytes |
//	+---------------+------------+----------+
func TestTxExecutedBytes(t *testing.T) {
	require := require.New(t)

	var (
		txID     = ids.ID{0x01}
		authHash = ids.ID{0x02}
	)
	msg, err := NewTxExecuted(txID, authHash)
	require.NoError(err)

	bytes := msg.Bytes()
	require.Len(bytes, 2+4+32+32)
	require.Equal(uint32(5), binary.BigEndian.Uint32(bytes[2:6]))
	require.Equal(txID[:], bytes[6:38])
	require.Equal(authHash[:], bytes[38:70])
}

// TestStakeSettledBytes pins the wire format:
//
//	+-----------------+------------+--------------------------+
//	|         codecID :     uint16 |                  2 bytes |
//	|          typeID :     uint32 |                  4 bytes |
//	|     stakingTxID :   [32]byte |                 32 bytes |
//	|   sourceChainID :   [32]byte |                 32 bytes |
//	|   sourceAddress :   [20]byte |                 20 bytes |
//	|      numRewards :     uint32 |                  4 bytes |
//	|     outputIndex :     uint32 |    4 bytes  ⎫            |
//	|          amount :     uint64 |    8 bytes  ⎭× numRewards|
//	+-----------------+------------+--------------------------+
//
// The Solidity side builds and reads these bytes by hand, so the layout - and
// the type id in particular - is an interface, not an implementation detail.
func TestStakeSettledBytes(t *testing.T) {
	require := require.New(t)

	var (
		stakingTxID   = ids.ID{0x01}
		sourceChainID = ids.ID{0x02}
		sourceAddress = ids.ShortID{0x03}
	)
	msg, err := NewStakeSettled(stakingTxID, sourceChainID, sourceAddress, []Reward{
		{OutputIndex: 7, Amount: 0x0102030405060708},
		{OutputIndex: 9, Amount: 1},
	})
	require.NoError(err)

	bytes := msg.Bytes()
	require.Len(bytes, 2+4+32+32+20+4+2*(4+8))
	require.Equal(uint32(6), binary.BigEndian.Uint32(bytes[2:6]))
	require.Equal(stakingTxID[:], bytes[6:38])
	require.Equal(sourceChainID[:], bytes[38:70])
	require.Equal(sourceAddress[:], bytes[70:90])
	require.Equal(uint32(2), binary.BigEndian.Uint32(bytes[90:94]))
	require.Equal(uint32(7), binary.BigEndian.Uint32(bytes[94:98]))
	require.Equal(uint64(0x0102030405060708), binary.BigEndian.Uint64(bytes[98:106]))
	require.Equal(uint32(9), binary.BigEndian.Uint32(bytes[106:110]))
	require.Equal(uint64(1), binary.BigEndian.Uint64(bytes[110:118]))

	parsed, err := ParseStakeSettled(bytes)
	require.NoError(err)
	require.Equal(msg, parsed)
}

// TestCycleSettledBytes pins the same layout, with rewardTxID in place of
// stakingTxID.
func TestCycleSettledBytes(t *testing.T) {
	require := require.New(t)

	var (
		rewardTxID    = ids.ID{0x01}
		sourceChainID = ids.ID{0x02}
		sourceAddress = ids.ShortID{0x03}
	)
	msg, err := NewCycleSettled(rewardTxID, sourceChainID, sourceAddress, nil)
	require.NoError(err)

	bytes := msg.Bytes()
	require.Len(bytes, 2+4+32+32+20+4)
	require.Equal(uint32(7), binary.BigEndian.Uint32(bytes[2:6]))
	require.Equal(rewardTxID[:], bytes[6:38])
	require.Equal(sourceChainID[:], bytes[38:70])
	require.Equal(sourceAddress[:], bytes[70:90])

	// An empty list is a legitimate value: "settled with no reward".
	require.Zero(binary.BigEndian.Uint32(bytes[90:94]))

	// A nil list and an empty one serialize identically, which is all that
	// matters for a quorum; the parsed value simply comes back as the latter.
	parsed, err := ParseCycleSettled(bytes)
	require.NoError(err)
	require.Empty(parsed.Rewards)
	require.Equal(bytes, parsed.Bytes())
	require.Equal(rewardTxID, parsed.RewardTxID)
	require.Equal(sourceChainID, parsed.SourceChainID)
	require.Equal(sourceAddress, parsed.SourceAddress)
}

// TestSettledRewardsSorted covers the ordering, which is what lets a quorum
// exist: without a normative order two nodes sign different bytes and the
// relayer never reaches 67%, with nothing failing anywhere.
func TestSettledRewardsSorted(t *testing.T) {
	tests := []struct {
		name        string
		rewards     []Reward
		expectedErr error
	}{
		{
			name: "empty",
		},
		{
			name:    "one",
			rewards: []Reward{{OutputIndex: 4}},
		},
		{
			name:    "increasing",
			rewards: []Reward{{OutputIndex: 4}, {OutputIndex: 5}, {OutputIndex: 9}},
		},
		{
			name:        "out of order",
			rewards:     []Reward{{OutputIndex: 5}, {OutputIndex: 4}},
			expectedErr: ErrRewardsNotSorted,
		},
		{
			// Two rewards cannot share an output index, so the order is
			// strictly increasing.
			name:        "duplicated index",
			rewards:     []Reward{{OutputIndex: 4}, {OutputIndex: 4}},
			expectedErr: ErrRewardsNotSorted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(t, (&StakeSettled{Rewards: test.rewards}).Verify(), test.expectedErr)
			require.ErrorIs(t, (&CycleSettled{Rewards: test.rewards}).Verify(), test.expectedErr)
		})
	}
}
