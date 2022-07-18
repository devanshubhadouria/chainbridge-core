// Copyright 2021 ChainSafe Systems
// SPDX-License-Identifier: LGPL-3.0-only

package executor

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"github.com/devanshubhadouria/chainbridge-core/chains/evm/calls"
	"github.com/devanshubhadouria/chainbridge-core/chains/evm/calls/consts"
	"github.com/devanshubhadouria/chainbridge-core/chains/evm/calls/transactor"
	"github.com/devanshubhadouria/chainbridge-core/types"

	"github.com/devanshubhadouria/chainbridge-core/chains/evm/executor/proposal"
	"github.com/devanshubhadouria/chainbridge-core/relayer/message"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethereumTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/rs/zerolog/log"
)

const (
	maxSimulateVoteChecks = 5
	maxShouldVoteChecks   = 40
	shouldVoteCheckPeriod = 15
)

var (
	Sleep = time.Sleep
)

type ChainClient interface {
	RelayerAddress() common.Address
	CallContract(ctx context.Context, callArgs map[string]interface{}, blockNumber *big.Int) ([]byte, error)
	SubscribePendingTransactions(ctx context.Context, ch chan<- common.Hash) (*rpc.ClientSubscription, error)
	TransactionByHash(ctx context.Context, hash common.Hash) (tx *ethereumTypes.Transaction, isPending bool, err error)
	calls.ContractCallerDispatcher
}

type MessageHandler interface {
	HandleMessage(m *message.Message) (*proposal.Proposal, error)
}

type BridgeContract interface {
	IsProposalVotedBy(by common.Address, p *proposal.Proposal) (bool, error)
	VoteProposal(proposal *proposal.Proposal, opts transactor.TransactOptions) (*common.Hash, error)
	VoteProposalforToken(proposal *proposal.Proposal, srcToken common.Address, opts transactor.TransactOptions) (*common.Hash, error)
	SimulateVoteProposal(proposal *proposal.Proposal) error
	ProposalStatus(p *proposal.Proposal) (message.ProposalStatus, error)
	GetThreshold() (uint8, error)
	AdminSetResource(handlerAddr common.Address,
		rID types.ResourceID,
		targetContractAddr common.Address, opts transactor.TransactOptions) (*common.Hash, error)
	SetBurnableInput(handlerAddr common.Address,
		tokenContractAddr common.Address,
		opts transactor.TransactOptions) (*common.Hash, error)
	IsProposalTokenVotedBy(by common.Address, p *proposal.Proposal) (bool, error)
	ProposalStatusToken(p *proposal.Proposal) (message.ProposalStatus, error)
	SimulateVoteProposalToken(proposal *proposal.Proposal, srcToken common.Address) error
	RemoveToken(handlerAddr common.Address, tokenContractAddr common.Address, resourceID types.ResourceID, opts transactor.TransactOptions) (*common.Hash, error)
	IsFeeClaimThresholdReached() (bool, error)
	RelayerClaimFees(
		destDomainID uint8,
		opts transactor.TransactOptions,
	) (*common.Hash, error)
}

type EVMVoter struct {
	mh                   MessageHandler
	client               ChainClient
	bridgeContract       BridgeContract
	pendingProposalVotes map[common.Hash]uint8
}

// NewVoterWithSubscription creates an instance of EVMVoter that votes for
// proposals on chain.
//
// It is created with a pending proposal subscription that listens to
// pending voteProposal transactions and avoids wasting gas on sending votes
// for transactions that will fail.
// Currently, officially supported only by Geth nodes.
func NewVoterWithSubscription(mh MessageHandler, client ChainClient, bridgeContract BridgeContract) (*EVMVoter, error) {
	voter := &EVMVoter{
		mh:                   mh,
		client:               client,
		bridgeContract:       bridgeContract,
		pendingProposalVotes: make(map[common.Hash]uint8),
	}

	ch := make(chan common.Hash)
	_, err := client.SubscribePendingTransactions(context.TODO(), ch)
	if err != nil {
		return nil, err
	}
	go voter.trackProposalPendingVotes(ch)

	return voter, nil
}

// NewVoter creates an instance of EVMVoter that votes for proposal on chain.
//
// It is created without pending proposal subscription and is a fallback
// for nodes that don't support pending transaction subscription and will vote
// on proposals that already satisfy threshold.
func NewVoter(mh MessageHandler, client ChainClient, bridgeContract BridgeContract) *EVMVoter {
	return &EVMVoter{
		mh:                   mh,
		client:               client,
		bridgeContract:       bridgeContract,
		pendingProposalVotes: make(map[common.Hash]uint8),
	}
}

// Execute checks if relayer already voted and is threshold
// satisfied and casts a vote if it isn't.
func (v *EVMVoter) Execute(m *message.Message) error {
	prop, err := v.mh.HandleMessage(m)
	if err != nil {
		return err
	}

	votedByTheRelayer, err := v.bridgeContract.IsProposalVotedBy(v.client.RelayerAddress(), prop)
	if err != nil {
		return err
	}
	if votedByTheRelayer {
		return nil
	}

	shouldVote, err := v.shouldVoteForProposal(prop, 0)
	if err != nil {
		log.Error().Err(err)
		return err
	}

	if !shouldVote {
		log.Debug().Msgf("Proposal %+v already satisfies threshold", prop)
		return nil
	}
	err = v.repetitiveSimulateVote(prop, 0)
	if err != nil {
		log.Error().Err(err)
		return err
	}

	hash, err := v.bridgeContract.VoteProposal(prop, transactor.TransactOptions{Priority: prop.Metadata.Priority})
	if err != nil {
		return fmt.Errorf("voting failed. Err: %w", err)
	}

	log.Debug().Str("hash", hash.String()).Uint64("nonce", prop.DepositNonce).Msgf("Voted")
	return nil
}

// shouldVoteForProposal checks if proposal already has threshold with pending
// proposal votes from other relayers.
// Only works properly in conjuction with NewVoterWithSubscription as without a subscription
// no pending txs would be received and pending vote count would be 0.
func (v *EVMVoter) shouldVoteForProposal(prop *proposal.Proposal, tries int) (bool, error) {
	propID := prop.GetID()
	defer delete(v.pendingProposalVotes, propID)

	// random delay to prevent all relayers checking for pending votes
	// at the same time and all of them sending another tx
	Sleep(time.Duration(rand.Intn(shouldVoteCheckPeriod)) * time.Second)

	ps, err := v.bridgeContract.ProposalStatus(prop)
	if err != nil {
		return false, err
	}

	if ps.Status == message.ProposalStatusExecuted || ps.Status == message.ProposalStatusCanceled {
		return false, nil
	}

	threshold, err := v.bridgeContract.GetThreshold()
	if err != nil {
		return false, err
	}

	if ps.YesVotesTotal+v.pendingProposalVotes[propID] >= threshold && tries < maxShouldVoteChecks {
		// Wait until proposal status is finalized to prevent missing votes
		// in case of dropped txs
		tries++
		log.Debug().Msgf("checking values", ps.YesVotesTotal)
		return v.shouldVoteForProposal(prop, tries)
	}

	return true, nil
}

func (v *EVMVoter) shouldVoteForProposalToken(prop *proposal.Proposal, tries int) (bool, error) {
	propID := prop.GetID()
	defer delete(v.pendingProposalVotes, propID)

	// random delay to prevent all relayers checking for pending votes
	// at the same time and all of them sending another tx
	Sleep(time.Duration(rand.Intn(shouldVoteCheckPeriod)) * time.Second)

	ps, err := v.bridgeContract.ProposalStatusToken(prop)
	if err != nil {
		return false, err
	}
	log.Debug().Msgf("checking values", ps.Status)
	if ps.Status == message.ProposalStatusExecuted || ps.Status == message.ProposalStatusCanceled {
		return false, nil
	}

	threshold, err := v.bridgeContract.GetThreshold()
	if err != nil {
		return false, err
	}

	if ps.YesVotesTotal+v.pendingProposalVotes[propID] >= threshold && tries < maxShouldVoteChecks {
		// Wait until proposal status is finalized to prevent missing votes
		// in case of dropped txs
		tries++
		log.Debug().Msgf("checking values", ps.YesVotesTotal, tries, maxShouldVoteChecks)
		return v.shouldVoteForProposalToken(prop, tries)
	}

	return true, nil
}

//Execute1
func (v *EVMVoter) Execute1(n *message.Message2) (bool, error) {

	prop := proposal.NewProposal1(n.Source, n.Destination, n.DepositNonce, n.ResourceId, []byte("fixed"), n.Desthandler, n.DestBridgeAddress, message.Metadata{})

	votedByTheRelayer, err := v.bridgeContract.IsProposalTokenVotedBy(v.client.RelayerAddress(), prop)
	if err != nil {
		return false, err
	}
	if votedByTheRelayer {
		return false, nil
	}

	shouldVote, err := v.shouldVoteForProposalToken(prop, 0)
	if err != nil {
		log.Error().Err(err)
		return false, err
	}

	if !shouldVote {
		log.Debug().Msgf("Proposal %+v already satisfies threshold", prop)

		return false, err
	}

	err = v.repetitiveSimulateVoteToken(prop, 0, n.DestTokenAddress)
	if err != nil {
		log.Error().Err(err)
		return false, err
	}

	hash, err := v.bridgeContract.VoteProposalforToken(prop, n.DestTokenAddress, transactor.TransactOptions{Priority: prop.Metadata.Priority})
	Sleep(time.Duration(200) * time.Second)
	ps, err := v.bridgeContract.ProposalStatusToken(prop)

	if err != nil {
		return false, err
	}

	log.Debug().Msgf("checking praposal", ps.Status)

	log.Debug().Str("hash", hash.String()).Uint64("nonce", prop.DepositNonce).Msgf("Voted")

	if err != nil {
		return false, fmt.Errorf("voting failed. Err: %w", err)
	}

	if ps.Status == message.ProposalStatusExecuted {

		a := v.executeOnchain(*hash)
		return a, nil
	}

	return false, nil
}

// repetitiveSimulateVote repeatedly tries(5 times) to simulate vore proposal call until it succeeds
func (v *EVMVoter) repetitiveSimulateVote(prop *proposal.Proposal, tries int) error {
	err := v.bridgeContract.SimulateVoteProposal(prop)
	if err != nil {
		if tries < maxSimulateVoteChecks {
			tries++
			return v.repetitiveSimulateVote(prop, tries)
		}
		return err
	} else {
		return nil
	}
}

func (v *EVMVoter) repetitiveSimulateVoteToken(prop *proposal.Proposal, tries int, src common.Address) error {
	err := v.bridgeContract.SimulateVoteProposalToken(prop, src)
	if err != nil {
		if tries < maxSimulateVoteChecks {
			tries++
			return v.repetitiveSimulateVoteToken(prop, tries, src)
		}
		return err
	} else {
		return nil
	}
}

// trackProposalPendingVotes tracks pending voteProposal txs from
// other relayers and increases count of pending votes in pendingProposalVotes map
// by proposal unique id.
func (v *EVMVoter) trackProposalPendingVotes(ch chan common.Hash) {
	for msg := range ch {
		txData, _, err := v.client.TransactionByHash(context.TODO(), msg)
		if err != nil {
			log.Error().Err(err)
			continue
		}

		a, err := abi.JSON(strings.NewReader(consts.BridgeABI))
		if err != nil {
			log.Error().Err(err)
			continue
		}

		if len(txData.Data()) < 4 {
			continue
		}

		m, err := a.MethodById(txData.Data()[:4])
		if err != nil {
			continue
		}

		data, err := m.Inputs.UnpackValues(txData.Data()[4:])
		if err != nil {
			log.Error().Err(err)
			continue
		}

		if m.Name == "voteProposal" {
			source := data[0].(uint8)
			depositNonce := data[1].(uint64)
			prop := proposal.Proposal{
				Source:       source,
				DepositNonce: depositNonce,
			}

			go v.increaseProposalVoteCount(msg, prop.GetID())
		}
		if m.Name == "voteProposalToken" {
			source := data[0].(uint8)
			depositNonce := data[1].(uint64)
			prop := proposal.Proposal{
				Source:       source,
				DepositNonce: depositNonce,
			}

			go v.increaseProposalVoteCount(msg, prop.GetID())
		}
	}
}

// increaseProposalVoteCount increases pending proposal vote for target proposal
// and decreases it when transaction is mined.
func (v *EVMVoter) increaseProposalVoteCount(hash common.Hash, propID common.Hash) {
	v.pendingProposalVotes[propID]++

	_, err := v.client.WaitAndReturnTxReceipt(hash)
	if err != nil {
		log.Error().Err(err)
	}

	v.pendingProposalVotes[propID]--
}

func (v *EVMVoter) executeOnchain(a common.Hash) bool {
	g, err := v.client.WaitAndReturnTxReceipt(a)
	if err != nil {
		log.Error().Err(err)
		return false
	}
	log.Debug().Msg("reached at destination")
	log.Debug().Msg(string(rune(g.Status)))
	return true
}

func (v *EVMVoter) ExecuteSourceTransactiions(p *message.Message2) error {

	err := v.SimulateTransactions(p, 0)
	if err != nil {
		return err
	}
	log.Debug().Msgf("Reached token registration at source")
	return nil
}

func (v *EVMVoter) SimulateTransactions(p *message.Message2, tries int64) error {

	hash, err := v.bridgeContract.AdminSetResource(p.Sourcehandler, p.ResourceId, p.SourceTokenAddress, transactor.TransactOptions{})
	log.Debug().Msgf(hash.String())
	if err != nil {
		if tries < maxSimulateVoteChecks {
			tries++
			return v.SimulateTransactions(p, tries)
		}
		return err
	} else {
		return nil
	}
}

func (v *EVMVoter) ExecuteRemovefromdest(p *message.Message2) error {
	hash, err := v.bridgeContract.RemoveToken(p.Desthandler, p.DestTokenAddress, p.ResourceId, transactor.TransactOptions{})
	if err != nil {
		log.Debug().Msgf(hash.String())
	}
	log.Debug().Msgf("Removed token successfully from source chain")
	return nil
}

func (v *EVMVoter) FeeClaimByRelayer(p *message.Message) error {
	hash, err := v.bridgeContract.RelayerClaimFees(p.Destination, transactor.TransactOptions{})
	if err != nil {
		log.Debug().Msgf(hash.String())
	}
	log.Debug().Msgf("fees claimed successfully")
	return nil
}

func (v *EVMVoter) IsFeeThresholdReached() bool {
	val, err := v.bridgeContract.IsFeeClaimThresholdReached()
	if err != nil {
		log.Debug().Msgf("Error in fetch fee claim threshold")
		return false
	}
	log.Debug().Msgf("Fetched fee claim threshold %t", val)
	return val
}
