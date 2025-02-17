package apptesting

import (
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	icatypes "github.com/cosmos/ibc-go/v3/modules/apps/27-interchain-accounts/types"
	transfertypes "github.com/cosmos/ibc-go/v3/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v3/modules/core/04-channel/types"
	ibctesting "github.com/cosmos/ibc-go/v3/testing"
	"github.com/cosmos/ibc-go/v3/testing/simapp"
	"github.com/stretchr/testify/suite"
	"github.com/tendermint/tendermint/crypto/ed25519"
	tmtypes "github.com/tendermint/tendermint/proto/tendermint/types"

	"github.com/Stride-Labs/stride/app"
)

var (
	StrideChainID = "STRIDE"

	TestIcaVersion = string(icatypes.ModuleCdc.MustMarshalJSON(&icatypes.Metadata{
		Version:                icatypes.Version,
		ControllerConnectionId: ibctesting.FirstConnectionID,
		HostConnectionId:       ibctesting.FirstConnectionID,
		Encoding:               icatypes.EncodingProtobuf,
		TxType:                 icatypes.TxTypeSDKMultiMsg,
	}))
)

type AppTestHelper struct {
	suite.Suite

	App     *app.StrideApp
	HostApp *simapp.SimApp

	IbcEnabled   bool
	Coordinator  *ibctesting.Coordinator
	StrideChain  *ibctesting.TestChain
	HostChain    *ibctesting.TestChain
	TransferPath *ibctesting.Path

	QueryHelper  *baseapp.QueryServiceTestHelper
	TestAccs     []sdk.AccAddress
	IcaAddresses map[string]string
}

// AppTestHelper Constructor
func (s *AppTestHelper) Setup() {
	s.App = app.InitStrideTestApp(true)
	s.QueryHelper = &baseapp.QueryServiceTestHelper{
		GRPCQueryRouter: s.App.GRPCQueryRouter(),
		Ctx:             s.Ctx(),
	}
	s.TestAccs = CreateRandomAccounts(3)
	s.IbcEnabled = false
	s.IcaAddresses = make(map[string]string)
}

// Dynamically gets the context of the Stride Chain
func (s *AppTestHelper) Ctx() sdk.Context {
	// If IBC support is enabled, return a dynamic context that updates with each block
	if s.IbcEnabled {
		return s.StrideChain.GetContext()
	}
	// Otherwise return a mock context
	return s.App.BaseApp.NewContext(false, tmtypes.Header{Height: 1, ChainID: StrideChainID})
}

// Dynamically gets the context of the Host Chain
func (s *AppTestHelper) HostCtx() sdk.Context {
	// Host context can only be used if IBC support has been enabled
	s.Require().NotNil(s.HostChain, "HostChain must be initialzed before accessing a context. "+
		"To initailze run `s.SetupIBCChains(chainID)`, `s.CreateTransferChannel(chainID)` or `s.CreateICAChannel(owner)`")

	return s.HostChain.GetContext()
}

// Mints coins directly to a module account
func (s *AppTestHelper) FundModuleAccount(moduleName string, amount sdk.Coin) {
	err := s.App.BankKeeper.MintCoins(s.Ctx(), moduleName, sdk.NewCoins(amount))
	s.Require().NoError(err)
}

// Mints and sends coins to a user account
func (s *AppTestHelper) FundAccount(acc sdk.AccAddress, amount sdk.Coin) {
	amountCoins := sdk.NewCoins(amount)
	err := s.App.BankKeeper.MintCoins(s.Ctx(), minttypes.ModuleName, amountCoins)
	s.Require().NoError(err)
	err = s.App.BankKeeper.SendCoinsFromModuleToAccount(s.Ctx(), minttypes.ModuleName, acc, amountCoins)
	s.Require().NoError(err)
}

// Helper function to compare coins with a more legible error
func (s *AppTestHelper) CompareCoins(expectedCoin sdk.Coin, actualCoin sdk.Coin, msg string) {
	s.Require().Equal(expectedCoin.Amount.Int64(), actualCoin.Amount.Int64(), msg)
}

// Generate random account addresss
func CreateRandomAccounts(numAccts int) []sdk.AccAddress {
	testAddrs := make([]sdk.AccAddress, numAccts)
	for i := 0; i < numAccts; i++ {
		pk := ed25519.GenPrivKey().PubKey()
		testAddrs[i] = sdk.AccAddress(pk.Address())
	}

	return testAddrs
}

// Initializes a ibctesting coordinator to keep track of Stride and a host chain's state
func (s *AppTestHelper) SetupIBCChains(hostChainID string) {
	s.Coordinator = ibctesting.NewCoordinator(s.T(), 0)

	// Initialize a stride testing app by casting a StrideApp -> TestingApp
	ibctesting.DefaultTestingAppInit = app.InitStrideIBCTestingApp
	s.StrideChain = ibctesting.NewTestChain(s.T(), s.Coordinator, StrideChainID)

	// Initialize a host testing app using SimApp -> TestingApp
	ibctesting.DefaultTestingAppInit = ibctesting.SetupTestingApp
	s.HostChain = ibctesting.NewTestChain(s.T(), s.Coordinator, hostChainID)

	// Update coordinator
	s.Coordinator.Chains = map[string]*ibctesting.TestChain{
		StrideChainID: s.StrideChain,
		hostChainID:   s.HostChain,
	}
	s.IbcEnabled = true
}

// Creates clients, connections, and a transfer channel between stride and a host chain
func (s *AppTestHelper) CreateTransferChannel(hostChainID string) {
	// If we have yet to create the host chain, do that here
	if !s.IbcEnabled {
		s.SetupIBCChains(hostChainID)
	}
	s.Require().Equal(s.HostChain.ChainID, hostChainID,
		fmt.Sprintf("The testing app has already been initialized with a different chainID (%s)", s.HostChain.ChainID))

	// Create clients, connections, and a transfer channel
	s.TransferPath = NewTransferPath(s.StrideChain, s.HostChain)
	s.Coordinator.Setup(s.TransferPath)

	// Replace stride and host apps with those from TestingApp
	s.App = s.StrideChain.App.(*app.StrideApp)
	s.HostApp = s.HostChain.GetSimApp()

	// Finally confirm the channel was setup properly
	s.Require().Equal(ibctesting.FirstClientID, s.TransferPath.EndpointA.ClientID, "stride clientID")
	s.Require().Equal(ibctesting.FirstConnectionID, s.TransferPath.EndpointA.ConnectionID, "stride connectionID")
	s.Require().Equal(ibctesting.FirstChannelID, s.TransferPath.EndpointA.ChannelID, "stride transfer channelID")

	s.Require().Equal(ibctesting.FirstClientID, s.TransferPath.EndpointB.ClientID, "host clientID")
	s.Require().Equal(ibctesting.FirstConnectionID, s.TransferPath.EndpointB.ConnectionID, "host connectionID")
	s.Require().Equal(ibctesting.FirstChannelID, s.TransferPath.EndpointB.ChannelID, "host transfer channelID")
}

// Creates an ICA channel through ibctesting
// Also creates a transfer channel is if hasn't been done yet
func (s *AppTestHelper) CreateICAChannel(owner string) {
	// If we have yet to create a client/connection (through creating a transfer channel), do that here
	_, transferChannelExists := s.App.IBCKeeper.ChannelKeeper.GetChannel(s.Ctx(), ibctesting.TransferPort, ibctesting.FirstChannelID)
	if !transferChannelExists {
		ownerSplit := strings.Split(owner, ".")
		s.Require().Equal(2, len(ownerSplit), "owner should be of the form: {HostZone}.{AccountName}")

		hostChainID := ownerSplit[0]
		s.CreateTransferChannel(hostChainID)
	}

	// Create ICA Path and then copy over the client and connection from the transfer path
	icaPath := NewIcaPath(s.StrideChain, s.HostChain)
	icaPath = CopyConnectionAndClientToPath(icaPath, s.TransferPath)

	// Register the ICA and complete the handshake
	s.RegisterInterchainAccount(icaPath.EndpointA, owner)

	err := icaPath.EndpointB.ChanOpenTry()
	s.Require().NoError(err, "ChanOpenTry error")

	err = icaPath.EndpointA.ChanOpenAck()
	s.Require().NoError(err, "ChanOpenAck error")

	err = icaPath.EndpointB.ChanOpenConfirm()
	s.Require().NoError(err, "ChanOpenConfirm error")

	// Confirm the ICA channel was created properly
	portID := icaPath.EndpointA.ChannelConfig.PortID
	channelID := icaPath.EndpointA.ChannelID
	_, found := s.App.IBCKeeper.ChannelKeeper.GetChannel(s.Ctx(), portID, channelID)
	s.Require().True(found, fmt.Sprintf("Channel not found after creation, PortID: %s, ChannelID: %s", portID, channelID))

	// Store the account address
	icaAddress, found := s.App.ICAControllerKeeper.GetInterchainAccountAddress(s.Ctx(), ibctesting.FirstConnectionID, portID)
	s.Require().True(found, "can't get ICA address")
	s.IcaAddresses[owner] = icaAddress

	// Finally set the active channel
	s.App.ICAControllerKeeper.SetActiveChannelID(s.Ctx(), ibctesting.FirstConnectionID, portID, channelID)
}

// Register's a new ICA account on the next channel available
// This function assumes a connection already exists
func (s *AppTestHelper) RegisterInterchainAccount(endpoint *ibctesting.Endpoint, owner string) {
	// Get the port ID from the owner name (i.e. "icacontroller-{owner}")
	portID, err := icatypes.NewControllerPortID(owner)
	s.Require().NoError(err, "owner to portID error")

	// Get the next channel available and register the ICA
	channelSequence := s.App.IBCKeeper.ChannelKeeper.GetNextChannelSequence(s.Ctx())

	err = s.App.ICAControllerKeeper.RegisterInterchainAccount(s.Ctx(), endpoint.ConnectionID, owner)
	s.Require().NoError(err, "register interchain account error")

	// Commit the state
	endpoint.Chain.App.Commit()
	endpoint.Chain.NextBlock()

	// Update the endpoint object to the newly created port + channel
	endpoint.ChannelID = channeltypes.FormatChannelIdentifier(channelSequence)
	endpoint.ChannelConfig.PortID = portID
}

// Creates a transfer channel between two chains
func NewTransferPath(chainA *ibctesting.TestChain, chainB *ibctesting.TestChain) *ibctesting.Path {
	path := ibctesting.NewPath(chainA, chainB)
	path.EndpointA.ChannelConfig.PortID = ibctesting.TransferPort
	path.EndpointB.ChannelConfig.PortID = ibctesting.TransferPort
	path.EndpointA.ChannelConfig.Order = channeltypes.UNORDERED
	path.EndpointB.ChannelConfig.Order = channeltypes.UNORDERED
	path.EndpointA.ChannelConfig.Version = transfertypes.Version
	path.EndpointB.ChannelConfig.Version = transfertypes.Version
	return path
}

// Creates an ICA channel between two chains
func NewIcaPath(chainA *ibctesting.TestChain, chainB *ibctesting.TestChain) *ibctesting.Path {
	path := ibctesting.NewPath(chainA, chainB)
	path.EndpointA.ChannelConfig.PortID = icatypes.PortID
	path.EndpointB.ChannelConfig.PortID = icatypes.PortID
	path.EndpointA.ChannelConfig.Order = channeltypes.ORDERED
	path.EndpointB.ChannelConfig.Order = channeltypes.ORDERED
	path.EndpointA.ChannelConfig.Version = TestIcaVersion
	path.EndpointB.ChannelConfig.Version = TestIcaVersion
	return path
}

// In ibctesting, there's no easy way to create a new channel on an existing connection
// To get around this, this helper function will copy the client/connection info from an existing channel
// We use this when creating ICA channels, because we want to reuse the same connections/clients from the transfer channel
func CopyConnectionAndClientToPath(path *ibctesting.Path, pathToCopy *ibctesting.Path) *ibctesting.Path {
	path.EndpointA.ClientID = pathToCopy.EndpointA.ClientID
	path.EndpointB.ClientID = pathToCopy.EndpointB.ClientID
	path.EndpointA.ConnectionID = pathToCopy.EndpointA.ConnectionID
	path.EndpointB.ConnectionID = pathToCopy.EndpointB.ConnectionID
	path.EndpointA.ClientConfig = pathToCopy.EndpointA.ClientConfig
	path.EndpointB.ClientConfig = pathToCopy.EndpointB.ClientConfig
	path.EndpointA.ConnectionConfig = pathToCopy.EndpointA.ConnectionConfig
	path.EndpointB.ConnectionConfig = pathToCopy.EndpointB.ConnectionConfig
	return path
}
