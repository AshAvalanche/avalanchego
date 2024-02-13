// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package chains

import (
	"crypto"
	"time"

	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/api/keystore"
	"github.com/ava-labs/avalanchego/api/metrics"
	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/snow/networking/sender"
	"github.com/ava-labs/avalanchego/snow/networking/timeout"
	"github.com/ava-labs/avalanchego/snow/networking/tracker"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/subnets"
	"github.com/ava-labs/avalanchego/trace"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms"
)

type FactoryConfig struct {
	NetworkID                 uint32
	PartialSyncPrimaryNetwork bool
	SybilProtectionEnabled    bool
	TracingEnabled            bool
	MeterVMEnabled            bool
	StateSyncBeacons          []ids.NodeID

	BootstrapMaxTimeGetAncestors            time.Duration
	BootstrapAncestorsMaxContainersSent     int
	BootstrapAncestorsMaxContainersReceived int

	FrontierPollFrequency   time.Duration
	ConsensusAppConcurrency int

	ApricotPhase4Time            time.Time
	ApricotPhase4MinPChainHeight uint64

	ChainDataDir  string
	AvaxAssetID   ids.ID
	SubnetConfigs map[ids.ID]subnets.Config
}

// Factory creates chains that are run by a node
type Factory struct {
	config FactoryConfig

	atomicMemory          *atomic.Memory
	stakingBLSKey         *bls.SecretKey
	stakingSigner         crypto.Signer
	db                    database.Database
	health                health.Health
	logFactory            logging.Factory
	log                   logging.Logger
	aliaser               ids.Aliaser
	xChainID              ids.ID
	cChainID              ids.ID
	nodeID                ids.NodeID
	keystore              keystore.Keystore
	msgCreator            message.Creator
	metrics               metrics.MultiGatherer
	router                router.Router
	net                   sender.ExternalSender
	blockAcceptorGroup    snow.AcceptorGroup
	txAcceptorGroup       snow.AcceptorGroup
	vertexAcceptorGroup   snow.AcceptorGroup
	stakingCert           *staking.Certificate
	timeoutManager        timeout.Manager
	tracer                trace.Tracer
	resourceTracker       tracker.ResourceTracker
	validators            validators.Manager
	vmManager             vms.Manager
	unblockChainCreatorCh chan struct{}
	subnets               *Subnets
	chainConfigs          map[string]ChainConfig

	// snowman++ related interface to allow validators retrieval
	validatorState validators.State
}

// NewFactory returns an instance of Factory
func NewFactory(
	config FactoryConfig,
	atomicMemory *atomic.Memory,
	stakingBLSKey *bls.SecretKey,
	stakingSigner crypto.Signer,
	db database.Database,
	health health.Health,
	logFactory logging.Factory,
	log logging.Logger,
	aliaser ids.Aliaser,
	xChainID ids.ID,
	cChainID ids.ID,
	nodeID ids.NodeID,
	keystore keystore.Keystore,
	msgCreator message.Creator,
	metrics metrics.MultiGatherer,
	router router.Router,
	net sender.ExternalSender,
	blockAcceptorGroup snow.AcceptorGroup,
	txAcceptorGroup snow.AcceptorGroup,
	vertexAcceptorGroup snow.AcceptorGroup,
	stakingCert *staking.Certificate,
	timeoutManager timeout.Manager,
	tracer trace.Tracer,
	resourceTracker tracker.ResourceTracker,
	validators validators.Manager,
	vmManager vms.Manager,
	unblockChainCreatorCh chan struct{},
	subnets *Subnets,
	chainConfigs map[string]ChainConfig,
) *Factory {
	return &Factory{
		config:                config,
		atomicMemory:          atomicMemory,
		stakingBLSKey:         stakingBLSKey,
		stakingSigner:         stakingSigner,
		db:                    db,
		health:                health,
		logFactory:            logFactory,
		log:                   log,
		aliaser:               aliaser,
		xChainID:              xChainID,
		cChainID:              cChainID,
		nodeID:                nodeID,
		keystore:              keystore,
		msgCreator:            msgCreator,
		metrics:               metrics,
		router:                router,
		net:                   net,
		blockAcceptorGroup:    blockAcceptorGroup,
		txAcceptorGroup:       txAcceptorGroup,
		vertexAcceptorGroup:   vertexAcceptorGroup,
		stakingCert:           stakingCert,
		timeoutManager:        timeoutManager,
		tracer:                tracer,
		resourceTracker:       resourceTracker,
		validators:            validators,
		vmManager:             vmManager,
		unblockChainCreatorCh: unblockChainCreatorCh,
		subnets:               subnets,
		chainConfigs:          chainConfigs,
	}
}
