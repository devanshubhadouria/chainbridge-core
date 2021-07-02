// Copyright 2021 ChainSafe Systems
// SPDX-License-Identifier: LGPL-3.0-only

package relayer

import (
	"net/http"

	"github.com/ChainSafe/chainbridge-core/metrics"
	"github.com/rs/zerolog/log"
)

type RelayedChain interface {
	PollEvents(stop <-chan struct{}, sysErr chan<- error, eventsChan chan *Message)
	Write(message *Message) error
	ChainID() uint8
}

func NewRelayer(chains []RelayedChain) *Relayer {
	return &Relayer{relayedChains: chains}
}

type Relayer struct {
	relayedChains []RelayedChain
	registry      map[uint8]RelayedChain
}

// Starts the relayer. Relayer routine is starting all the chains
// and passing them with a channel that accepts unified cross chain message format
func (r *Relayer) Start(stop <-chan struct{}, sysErr chan error) {
	log.Debug().Msgf("Starting relayer")
	messagesChannel := make(chan *Message)

	// init new instance of ChainMetrics
	chainMetrics := metrics.New()

	// register /metrics endpoint
	http.HandleFunc("/metrics", chainMetrics.MetricsHandler)

	// start listener in goroutine so non-blocking
	go http.ListenAndServe(":2112", nil)
	log.Info().Msg("Metrics server listening at: http://localhost:2112/metrics")

	for _, c := range r.relayedChains {
		log.Debug().Msgf("Starting chain %v", c.ChainID())
		r.addRelayedChain(c)
		go c.PollEvents(stop, sysErr, messagesChannel)
	}
	for {
		select {
		case m := <-messagesChannel:
			go r.route(m, chainMetrics)
			continue
		case _ = <-stop:
			return
		}
	}
}

// Route function winds destination writer by mapping DestinationID from message to registered writer.
func (r *Relayer) route(m *Message, chainMetrics *metrics.ChainMetrics) {
	w, ok := r.registry[m.Destination]
	if !ok {
		log.Error().Msgf("no resolver for destID %v to send message registered", m.Destination)
		return
	}

	// extract amount transferred from message
	payloadAmount, err := extractAmountTransferred(m)
	if err != nil {
		log.Error().Err(err)
		return
	}

	// increment chain metrics
	chainMetrics.TotalAmountTransferred += int(payloadAmount)
	chainMetrics.TotalNumberOfTransfers += 1

	log.Debug().Msgf("Sending message %+v to destination %v", m, m.Destination)
	if err := w.Write(m); err != nil {
		log.Error().Err(err).Msgf("%v", m)
		return
	}
}

func (r *Relayer) addRelayedChain(c RelayedChain) {
	if r.registry == nil {
		r.registry = make(map[uint8]RelayedChain)
	}
	chainID := c.ChainID()
	r.registry[chainID] = c
}
