package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	featureSaveKeys uint32 = 1 << iota
	featureOpenCode
	featurePromptHistory
	featureContextSelection
	featureHistoryPages
	featureContextFileSelection
)

type gatewaySupervisor struct {
	active  atomic.Pointer[HTTP]
	mu      sync.Mutex
	release func()
	connect func(context.Context) (*HTTP, func(), error)
}

// SuperviseManaged keeps one client handle across discoveries of the local gateway.
func SuperviseManaged(active *HTTP, release func(), connect func(context.Context) (*HTTP, func(), error)) *HTTP {
	supervisor := &gatewaySupervisor{release: release, connect: connect}
	supervisor.active.Store(active)
	return &HTTP{supervisor: supervisor}
}

func (c *HTTP) endpoint() *HTTP {
	if c.supervisor != nil {
		return c.supervisor.active.Load()
	}
	return c
}

func gatewayFeatures(header string) (features uint32) {
	for _, name := range strings.Split(header, ",") {
		switch strings.TrimSpace(name) {
		case "idempotent-saves":
			features |= featureSaveKeys
		case "opencode":
			features |= featureOpenCode
		case "prompt-history":
			features |= featurePromptHistory
		case "context-selection":
			features |= featureContextSelection
		case "history-pages":
			features |= featureHistoryPages
		case "context-file-selection":
			features |= featureContextFileSelection
		}
	}
	return features
}

func (c *HTTP) supportsSaveKeys() bool {
	return c.hasFeature(featureSaveKeys)
}

func (c *HTTP) SupportsContextSelection() bool {
	return c.hasFeature(featureContextSelection)
}

func (c *HTTP) SupportsContextFileSelection() bool {
	return c.hasFeature(featureContextFileSelection)
}

func (c *HTTP) hasFeature(feature uint32) bool {
	active := c.endpoint()
	return active != nil && active.features.Load()&feature != 0
}

func (c *HTTP) Reconnect(ctx context.Context) error {
	if c.supervisor == nil {
		return nil
	}
	_, err := c.supervisor.reconnect(ctx, c.endpoint())
	return err
}

func (s *gatewaySupervisor) reconnect(ctx context.Context, failed *HTTP) (*HTTP, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.active.Load()
	if active == nil {
		return nil, errors.New("gateway client is closed")
	}
	if active != failed {
		return active, nil
	}
	replacement, release, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	s.active.Store(replacement)
	s.release()
	s.release = release
	return replacement, nil
}

func (s *gatewaySupervisor) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active.Swap(nil) != nil {
		s.release()
	}
}

func (s *gatewaySupervisor) request(ctx context.Context, method, path string, body any, key string) (*http.Response, error) {
	active := s.active.Load()
	if active == nil {
		return nil, errors.New("gateway client is closed")
	}
	response, err := active.request(ctx, method, path, body, key)
	var failure *GatewayError
	if !errors.As(err, &failure) || (failure.Code != "gateway_unavailable" && failure.Code != "invalid_api_key") {
		return response, err
	}
	replacement, reconnectErr := s.reconnect(ctx, active)
	if reconnectErr == nil && (method == http.MethodGet || key != "" && active.supportsSaveKeys() && replacement.supportsSaveKeys()) {
		return replacement.request(ctx, method, path, body, key)
	}
	return response, err
}
