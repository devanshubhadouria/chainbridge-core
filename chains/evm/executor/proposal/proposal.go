package proposal

import (
	"github.com/devanshubhadouria/chainbridge-core/relayer/message"
	"github.com/devanshubhadouria/chainbridge-core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func NewProposal(source, destination uint8, depositNonce uint64, resourceId types.ResourceID, data []byte, handlerAddress, bridgeAddress common.Address, metadata message.Metadata) *Proposal {

	return &Proposal{
		Source:         source,
		Destination:    destination,
		DepositNonce:   depositNonce,
		ResourceId:     resourceId,
		Data:           data,
		HandlerAddress: handlerAddress,
		BridgeAddress:  bridgeAddress,
		Metadata:       metadata,
	}
}

type Proposal struct {
	Source         uint8  // Source domainID where message was initiated
	Destination    uint8  // Destination domainID where message is to be sent
	DepositNonce   uint64 // Nonce for the deposit
	ResourceId     types.ResourceID
	Metadata       message.Metadata
	Data           []byte
	HandlerAddress common.Address
	BridgeAddress  common.Address
}

func NewProposal1(source, destination uint8, depositNonce uint64, resourceId types.ResourceID, data []byte, handlerAddress, bridgeAddress common.Address, metadata message.Metadata) *Proposal {

	return &Proposal{
		Source:         source,
		Destination:    destination,
		DepositNonce:   depositNonce,
		ResourceId:     resourceId,
		Data:           data,
		HandlerAddress: handlerAddress,
		BridgeAddress:  bridgeAddress,
		Metadata:       metadata,
	}
}

type Proposal1 struct {
	Source         uint8  // Source domainID where message was initiated
	Destination    uint8  // Destination domainID where message is to be sent
	DepositNonce   uint64 // Nonce for the deposit
	ResourceId     types.ResourceID
	Data           []byte
	HandlerAddress common.Address
	BridgeAddress  common.Address
}

// GetDataHash constructs and returns proposal data hash
func (p *Proposal) GetDataHash() common.Hash {
	return crypto.Keccak256Hash(append(p.HandlerAddress.Bytes(), p.Data...))
}
func (p *Proposal) GetDataHash2() common.Hash {
	return common.HexToHash("0x5380c7b7ae81a58eb98d9c78de4a1fd7fd9535fc953ed2be602daaa41767312a")
}

// GetID constructs proposal unique identifier
func (p *Proposal) GetID() common.Hash {
	return crypto.Keccak256Hash(append([]byte{p.Source}, byte(p.DepositNonce)))
}
