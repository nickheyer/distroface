package services

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"google.golang.org/protobuf/types/known/structpb"
)

var _ distrofacev1connect.ConfigurationServiceHandler = (*ConfigurationService)(nil)

// Only these config keys leave the public rpc
var publicKeys = []string{
	"server.hostname",
}

type ConfigurationService struct {
	config *config.Config
	log    *logger.Logger
}

func NewConfigurationService(cfg *config.Config, log *logger.Logger) *ConfigurationService {
	return &ConfigurationService{config: cfg, log: log}
}

func (s *ConfigurationService) GetConfiguration(ctx context.Context, req *connect.Request[v1.GetConfigurationRequest]) (*connect.Response[v1.GetConfigurationResponse], error) {
	flat, err := flattenConfig(s.config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshalling config: %w", err))
	}

	var entries []*v1.ConfigEntry
	for key, val := range flat {
		if !slices.Contains(publicKeys, key) {
			continue
		}
		pbVal, err := structpb.NewValue(val)
		if err != nil {
			s.log.Warn("skipping config key %s: %v", key, err)
			continue
		}
		entries = append(entries, &v1.ConfigEntry{Key: key, Value: pbVal})
	}

	return connect.NewResponse(&v1.GetConfigurationResponse{Entries: entries}), nil
}

// Marshals the config struct
func flattenConfig(cfg *config.Config) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]any)
	flatten("", raw, out)
	return out, nil
}

func flatten(prefix string, src map[string]any, dst map[string]any) {
	for k, v := range src {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			flatten(key, nested, dst)
		} else {
			dst[key] = v
		}
	}
}
