// Copyright 2021-2026 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package restful

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// loadBalanceDNSCacheTTL is the per-Client DNS cache lifetime used by
// EnableLoadBalanceRandom. Tests may set this to zero to disable caching.
var loadBalanceDNSCacheTTL = 2 * time.Second

type lbIPCtxKey struct{}

type lbDNSCache struct {
	mu    sync.Mutex
	host  string
	ips   []string
	err   error
	until time.Time
}

type loadBalanceTransport struct {
	client  *Client
	wrapped http.RoundTripper
	mu      sync.Mutex
	pinned  map[string]*http.Transport
}

func withLoadBalanceIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, lbIPCtxKey{}, ip)
}

func loadBalanceIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(lbIPCtxKey{}).(string)
	return ip
}

func (t *loadBalanceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ip := loadBalanceIPFromContext(req.Context())
	if ip == "" {
		ip = t.client.pickLoadBalanceIP(req, req.URL.Hostname())
	}
	if ip == "" {
		return t.wrapped.RoundTrip(req)
	}
	rt := t.pinnedTransport(ip)
	if rt == nil {
		return t.wrapped.RoundTrip(req)
	}
	return rt.RoundTrip(req)
}

func (t *loadBalanceTransport) pinnedTransport(ip string) http.RoundTripper {
	base, ok := t.wrapped.(*http.Transport)
	if !ok {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pinned == nil {
		t.pinned = make(map[string]*http.Transport)
	}
	clone, ok := t.pinned[ip]
	if !ok {
		clone = pinTransportToIP(base, ip)
		t.pinned[ip] = clone
	} else {
		clone.TLSClientConfig = base.TLSClientConfig
	}
	return clone
}

func pinTransportToIP(base *http.Transport, ip string) *http.Transport {
	clone := base.Clone()
	clone.TLSClientConfig = base.TLSClientConfig
	clone.DialContext = pinDialContext(ip, dialContextOrDefault(clone.DialContext))
	if clone.DialTLSContext != nil {
		orig := clone.DialTLSContext
		clone.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			rewritten, err := replaceDialHost(addr, ip)
			if err != nil {
				return nil, err
			}
			return orig(ctx, network, rewritten)
		}
	}
	return clone
}

func dialContextOrDefault(d func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	if d != nil {
		return d
	}
	return (&net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second}).DialContext
}

func pinDialContext(ip string, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		rewritten, err := replaceDialHost(addr, ip)
		if err != nil {
			return nil, err
		}
		return dial(ctx, network, rewritten)
	}
}

func replaceDialHost(addr, ip string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip, port), nil
}

func (c *Client) applyLoadBalance(req *http.Request, target, originalHost string) (string, *http.Request) {
	ip := c.pickLoadBalanceIP(req, originalHost)
	if ip == "" {
		return target, req
	}
	return target + "[" + ip + "]", req.WithContext(withLoadBalanceIP(req.Context(), ip))
}

func (c *Client) pickLoadBalanceIP(req *http.Request, originalHost string) string {
	if !c.LoadBalanceRandom {
		return ""
	}
	if net.ParseIP(originalHost) != nil {
		log.Debugf("Host %s is an IP address, not a hostname. Load balancing is not applied.", originalHost)
		return ""
	}

	ips, err := c.lookupLoadBalanceIPs(req.Context(), originalHost)
	if err != nil {
		log.Debugf("Failed to resolve host %s: %v", originalHost, err)
		return ""
	}
	if len(ips) <= 1 {
		return ""
	}
	ip := chooseIPFromList(ips)
	log.Debugf("Multiple IPs for %s: %v, chosen %s", originalHost, ips, ip)
	return ip
}

func (c *Client) lookupLoadBalanceIPs(ctx context.Context, host string) ([]string, error) {
	ttl := loadBalanceDNSCacheTTL
	if ttl > 0 {
		c.lbDNS.mu.Lock()
		if c.lbDNS.host == host && time.Now().Before(c.lbDNS.until) {
			ips, err := c.lbDNS.ips, c.lbDNS.err
			c.lbDNS.mu.Unlock()
			return ips, err
		}
		c.lbDNS.mu.Unlock()
	}

	ips, err := netLookupHost(ctx, host)
	if ttl > 0 {
		c.lbDNS.mu.Lock()
		c.lbDNS.host = host
		c.lbDNS.ips = ips
		c.lbDNS.err = err
		c.lbDNS.until = time.Now().Add(ttl)
		c.lbDNS.mu.Unlock()
	}
	return ips, err
}

func (c *Client) setLoadBalanceTarget(req *http.Request, target, originalHost string) string {
	out, _ := c.applyLoadBalance(req, target, originalHost)
	return out
}
