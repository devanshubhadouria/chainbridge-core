package listener

import (
	"context"
	"fmt"
	"math/big"

	"github.com/devanshubhadouria/chainbridge-core/chains/evm/calls/events"
	"github.com/devanshubhadouria/chainbridge-core/relayer/message"
	"github.com/devanshubhadouria/chainbridge-core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"
)

type EventListener interface {
	FetchDeposits(ctx context.Context, address common.Address, startBlock *big.Int, endBlock *big.Int) ([]*events.Deposit, error)
	FetchRegisterEvents(ctx context.Context, address common.Address, startBlock *big.Int, endBlock *big.Int) ([]*events.RegisterToken, error)
}

type DepositHandler interface {
	HandleDeposit(sourceID, destID uint8, nonce uint64, resourceID types.ResourceID, calldata, handlerResponse []byte) (*message.Message, error)
}

type DepositEventHandler struct {
	eventListener  EventListener
	depositHandler DepositHandler
	bridgeAddress  common.Address
	domainID       uint8
}

func NewDepositEventHandler(eventListener EventListener, depositHandler DepositHandler, bridgeAddress common.Address, domainID uint8) *DepositEventHandler {
	return &DepositEventHandler{
		eventListener:  eventListener,
		depositHandler: depositHandler,
		bridgeAddress:  bridgeAddress,
		domainID:       domainID,
	}
}

func (eh *DepositEventHandler) HandleEvent(block *big.Int, msgChan chan *message.Message, msgChan1 chan *message.Message2) error {
	deposits, err := eh.eventListener.FetchDeposits(context.Background(), eh.bridgeAddress, block, block)
	if err != nil {
		return fmt.Errorf("unable to fetch deposit events because of: %+v", err)
	}

	for _, d := range deposits {
		m, err := eh.depositHandler.HandleDeposit(eh.domainID, d.DestinationDomainID, d.DepositNonce, d.ResourceID, d.Data, d.HandlerResponse)
		if err != nil {
			log.Error().Str("block", block.String()).Uint8("domainID", eh.domainID).Msgf("%v", err)
			continue
		}
		log.Debug().Msgf("Resolved message %+v in block %s", m, block.String())
		msgChan <- m
	}
	deposit1, err := eh.eventListener.FetchRegisterEvents(context.Background(), eh.bridgeAddress, block, block)
	if err != nil {
		return fmt.Errorf("unable to fetch deposit events because of: %+v", err)
	}
	for _, o := range deposit1 {
		value := [32]byte{}
		g := crypto.Keccak256Hash(append([]byte{o.DomainId}, byte(o.DepositNounce))).String()
		copy(value[:], []byte(g))
		n := message.NewMessage1(o.DomainId, o.DestinationDomainId, o.DepositNounce, value, o.SourceHandler, o.DestHandler, o.DestBridgeContract, o.SourceBridgeContract, o.SourceToken, o.DestToken)

		if err != nil {
			log.Error().Str("block", block.String()).Uint8("domainID", eh.domainID).Msgf("%v", err)
			continue
		}

		log.Debug().Msgf("Resolved message %+v in block %s", n, block.String())

		msgChan1 <- n
	}
	log.Debug().Msgf("Queried block  %s", block.String())
	return nil
}
