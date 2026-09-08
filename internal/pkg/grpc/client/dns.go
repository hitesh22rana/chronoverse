package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/resolver"
	grpcDNS "google.golang.org/grpc/resolver/dns"
)

// dnsPollScheme is a drop-in wrapper around the stock "dns" resolver that
// re-resolves on a timer. Stock dns only re-resolves on ResolveNow, which
// fires on connection failures, so with healthy long-lived connections a
// client keeps its dial-time membership forever and scaled-up pods stay
// invisible (and idle) until a restart. The timer bounds that skew to one
// interval.
const dnsPollScheme = "dns-poll"

const (
	dnsPollInterval          = 15 * time.Second
	dnsMinResolutionInterval = 10 * time.Second
)

var registerDNSPollOnce sync.Once

// ensureDNSPollRegistered registers the polling DNS resolver once: the
// registry panics on duplicates and every process dials several services.
// It also lowers the re-resolution floor, otherwise the 15s tick is
// throttled by the 30s stock default.
func ensureDNSPollRegistered() {
	registerDNSPollOnce.Do(func() {
		grpcDNS.SetMinResolutionInterval(dnsMinResolutionInterval)
		resolver.Register(dnsPollBuilder{})
	})
}

type dnsPollBuilder struct{}

func (dnsPollBuilder) Scheme() string { return dnsPollScheme }

func (dnsPollBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	target.URL.Scheme = "dns"
	r, err := resolver.Get("dns").Build(target, cc, opts)
	if err != nil {
		return nil, err
	}

	ctx, stop := context.WithCancel(context.Background())
	pr := &dnsPollResolver{Resolver: r, stop: stop}
	go pollResolveNow(ctx, r)
	return pr, nil
}

type dnsPollResolver struct {
	resolver.Resolver
	stop context.CancelFunc
}

func (r *dnsPollResolver) Close() {
	r.stop()
	r.Resolver.Close()
}

// pollResolveNow forces DNS re-resolution on every tick until ctx is done.
func pollResolveNow(ctx context.Context, r resolver.Resolver) {
	t := time.NewTicker(dnsPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.ResolveNow(resolver.ResolveNowOptions{})
		}
	}
}

// buildDialTarget normalizes service host config into a dns-poll target.
// Host arrives as "dns:///svc.ns.svc" on Kubernetes and as a bare "svc" on
// compose, so strip any scheme and always dial through dns-poll. The triple
// slash matters: gRPC resolves the endpoint from the URL path, so the host
// must sit in the path slot ("dns-poll:///host:port"), not the authority
// slot ("dns-poll://host:port", which resolves to an empty endpoint and
// leaves the channel with zero addresses).
func buildDialTarget(host string, port int) string {
	host = strings.TrimPrefix(strings.TrimPrefix(host, "dns:///"), "dns://")
	return fmt.Sprintf("%s:///%s:%d", dnsPollScheme, host, port)
}
