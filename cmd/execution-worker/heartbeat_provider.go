package main

import (
	"github.com/hitesh22rana/chronoverse/internal/pkg/kind/heartbeat"
)

// newHeartbeatProvider configures Pref64 before constructing the guarded client.
func newHeartbeatProvider(prefixes []string) (*heartbeat.HeartBeat, error) {
	if err := heartbeat.ConfigureNAT64Prefixes(prefixes); err != nil {
		return nil, err
	}
	return heartbeat.New(), nil
}
