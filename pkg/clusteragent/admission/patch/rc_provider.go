// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build kubeapiserver
// +build kubeapiserver

package patch

import (
	"encoding/json"
	"errors"

	"github.com/DataDog/datadog-agent/pkg/config/remote"
	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

// remoteConfigProvider consumes tracing configs from RC and delivers them to the patcher
type remoteConfigProvider struct {
	client      *remote.Client
	isLeader    func() bool
	subscribers map[TargetObjKind]chan PatchRequest
	clusterName string
}

var _ patchProvider = &remoteConfigProvider{}

func newRemoteConfigProvider(client *remote.Client, isLeaderFunc func() bool, clusterName string) (*remoteConfigProvider, error) {
	if client == nil {
		return nil, errors.New("remote config client not initialized")
	}
	return &remoteConfigProvider{
		client:      client,
		isLeader:    isLeaderFunc,
		subscribers: make(map[TargetObjKind]chan PatchRequest),
		clusterName: clusterName,
	}, nil
}

func (rcp *remoteConfigProvider) start(stopCh <-chan struct{}) {
	log.Info("Starting RC patch provider")
	rcp.client.RegisterAPMTracing(rcp.process)
	rcp.client.Start()
	<-stopCh
	log.Info("Shutting down RC patch provider")
	rcp.client.Close()
}

func (rcp *remoteConfigProvider) subscribe(kind TargetObjKind) chan PatchRequest {
	ch := make(chan PatchRequest, 10)
	rcp.subscribers[kind] = ch
	return ch
}

// process is the event handler called by the RC client on config updates
func (rcp *remoteConfigProvider) process(update map[string]state.APMTracingConfig) {
	log.Infof("Got %d updates from RC", len(update))
	for path, config := range update {
		log.Debugf("Parsing config %s with metadata %+v from path %s", config.Config, config.Metadata, path)
		var req PatchRequest
		err := json.Unmarshal(config.Config, &req)
		if err != nil {
			log.Errorf("Error while parsing config: %v", err)
			continue
		}
		log.Debugf("Patch request parsed %+v", req)
		if err := req.Validate(rcp.clusterName); err != nil {
			log.Errorf("Skipping invalid patch request: %s", err)
			continue
		}
		if ch, found := rcp.subscribers[req.K8sTarget.Kind]; found {
			log.Debugf("Publishing patch request for target %s", req.K8sTarget)
			ch <- req
		}
	}
}