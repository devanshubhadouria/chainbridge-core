package events

import (
	"context"
	"math/big"
	"strings"

	"github.com/devanshubhadouria/chainbridge-core/chains/evm/calls/consts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog/log"
)

type ChainClient interface {
	FetchEventLogs(ctx context.Context, contractAddress common.Address, event string, startBlock *big.Int, endBlock *big.Int) ([]ethTypes.Log, error)
}

type Listener struct {
	client ChainClient
	abi    abi.ABI
}

func NewListener(client ChainClient) *Listener {
	abi, _ := abi.JSON(strings.NewReader(consts.BridgeABI))
	return &Listener{
		client: client,
		abi:    abi,
	}
}

func (l *Listener) FetchDeposits(ctx context.Context, contractAddress common.Address, startBlock *big.Int, endBlock *big.Int) ([]*Deposit, error) {
	logs, err := l.client.FetchEventLogs(ctx, contractAddress, string(DepositSig), startBlock, endBlock)
	if err != nil {
		return nil, err
	}
	deposits := make([]*Deposit, 0)

	for _, dl := range logs {
		d, err := l.UnpackDeposit(l.abi, dl.Data)
		if err != nil {
			log.Error().Msgf("failed unpacking deposit event log: %v", err)
			continue
		}

		d.SenderAddress = common.BytesToAddress(dl.Topics[1].Bytes())
		log.Debug().Msgf("Found deposit log in block: %d, TxHash: %s, contractAddress: %s, sender: %s", dl.BlockNumber, dl.TxHash, dl.Address, d.SenderAddress)

		deposits = append(deposits, d)
	}

	return deposits, nil
}

func (l *Listener) FetchRegisterEvents(ctx context.Context, contractAddress common.Address, startBlock *big.Int, endBlock *big.Int) ([]*RegisterToken, error) {
	logs, err := l.client.FetchEventLogs(ctx, contractAddress, string(RegisterTokenSig), startBlock, endBlock)
	if err != nil {
		return nil, err
	}
	deposits := make([]*RegisterToken, 0)
	log.Debug().Msgf("Found registertoken  log in block:", logs)
	for _, dl := range logs {

		c, err := l.UnpackRegisterToken(l.abi, dl.Data)

		if err != nil {
			log.Error().Msgf("failed unpacking deposit event log: %v", err)
			continue
		}
		log.Debug().Msgf("Found register token log in block: %d, TxHash: %s, contractAddress: %s, sender: %s", dl.BlockNumber, dl.TxHash, dl.Address)

		deposits = append(deposits, c)
	}

	return deposits, nil
}

func (l *Listener) UnpackDeposit(abi abi.ABI, data []byte) (*Deposit, error) {
	var dl Deposit

	err := abi.UnpackIntoInterface(&dl, "Deposit", data)
	if err != nil {
		return &Deposit{}, err
	}

	return &dl, nil
}

func (l *Listener) UnpackRegisterToken(abi abi.ABI, data []byte) (*RegisterToken, error) {
	var dl RegisterToken

	err := abi.UnpackIntoInterface(&dl, "RegisterToken", data)
	if err != nil {
		return &RegisterToken{}, err
	}

	return &dl, nil
}
