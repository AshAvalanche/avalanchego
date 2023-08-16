// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package linearcodec

import (
	"testing"

	"github.com/ava-labs/avalanchego/codec"
)

func TestVectors(t *testing.T) {
	for _, test := range codec.Tests {
		c := NewDefault()
		test(c, t)
	}
}

func TestMultipleTags(t *testing.T) {
	for _, test := range codec.MultipleTagsTests {
		c := New([]string{"tag1", "tag2"}, DefaultMaxSliceLength)
		test(c, t)
	}
}

func FuzzVectors(f *testing.F) {
	for _, test := range codec.FuzzTests {
		c := NewDefault()
		test(c, f)
	}
}
