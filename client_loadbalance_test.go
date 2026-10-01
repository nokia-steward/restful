// Copyright 2021-2026 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package restful

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lbTestHost = "lb.test.invalid"

func TestLoadBalanceRandom_TLSSNIUsesHostnameNotIP(t *testing.T) {
	cert, pool := mustDNSSANOnlyCert(t, lbTestHost)

	var sniMu sync.Mutex
	var gotSNI string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sniMu.Lock()
			gotSNI = hello.ServerName
			sniMu.Unlock()
			return nil, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	serverIP := srv.Listener.Addr().(*net.TCPAddr).IP.String()

	origLookup := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		assert.Equal(t, lbTestHost, host)
		return []string{serverIP, serverIP}, nil
	}
	defer func() { netLookupHost = origLookup }()

	var connectedAddr string
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		ConnectStart: func(network, addr string) {
			connectedAddr = addr
		},
	})

	target := "https://" + net.JoinHostPort(lbTestHost, strconv.Itoa(port)) + "/resource"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	require.NoError(t, err)
	origURLHost := req.URL.Host

	client := NewClient().EnableLoadBalanceRandom(true)
	client.TLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, origURLHost, req.URL.Host, "request URL host must stay the hostname so TLS verifies the DNS SAN")
	sniMu.Lock()
	sni := gotSNI
	sniMu.Unlock()
	assert.Equal(t, lbTestHost, sni, "SNI must be the hostname, not an IP")
	require.NotEmpty(t, connectedAddr)
	connHost, _, err := net.SplitHostPort(connectedAddr)
	require.NoError(t, err)
	assert.Equal(t, serverIP, connHost, "TCP connection must go to a resolved address, not the hostname")

	tr, ok := client.GetTransport().(*http.Transport)
	require.True(t, ok)
	if tr.TLSClientConfig != nil {
		assert.Empty(t, tr.TLSClientConfig.ServerName)
	}
}

func TestLoadBalanceRandom_DistributesAcrossAddresses(t *testing.T) {
	ln1, ln2 := listenLoopbackPair(t)
	var n1, n2 atomic.Int32
	serveHTTPOn(t, ln1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n1.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	serveHTTPOn(t, ln2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	ip1 := ln1.Addr().(*net.TCPAddr).IP.String()
	ip2 := ln2.Addr().(*net.TCPAddr).IP.String()
	port := ln1.Addr().(*net.TCPAddr).Port
	patchLookupHost(t, ip1, ip2)

	client := NewClient().EnableLoadBalanceRandom(true)
	target := "http://" + net.JoinHostPort(lbTestHost, strconv.Itoa(port)) + "/"
	for range 50 {
		require.NoError(t, client.Get(context.Background(), target, nil))
	}
	assert.NotZero(t, n1.Load(), "expected traffic to %s", ip1)
	assert.NotZero(t, n2.Load(), "expected traffic to %s", ip2)
}

func TestLoadBalanceRandom_RetryRepicksAddress(t *testing.T) {
	ln1, ln2 := listenLoopbackPair(t)
	var n1, n2 atomic.Int32
	serveHTTPOn(t, ln1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n1.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	serveHTTPOn(t, ln2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	ip1 := ln1.Addr().(*net.TCPAddr).IP.String()
	ip2 := ln2.Addr().(*net.TCPAddr).IP.String()
	port := ln1.Addr().(*net.TCPAddr).Port
	patchLookupHost(t, ip1, ip2)

	origChoose := chooseIPFromList
	n := 0
	chooseIPFromList = func(IPs []string) string {
		i := n
		n++
		if i == 0 {
			return IPs[0]
		}
		return IPs[1]
	}
	t.Cleanup(func() { chooseIPFromList = origChoose })

	client := NewClient().EnableLoadBalanceRandom(true).Retry(1, time.Millisecond, 5*time.Millisecond)
	target := "http://" + net.JoinHostPort(lbTestHost, strconv.Itoa(port)) + "/"
	require.NoError(t, client.Get(context.Background(), target, nil))
	assert.Equal(t, int32(1), n1.Load(), "first attempt should hit the 503 backend")
	assert.Equal(t, int32(1), n2.Load(), "retry should hit the 200 backend")
}

func TestLoadBalanceRandom_DNSCache(t *testing.T) {
	ln1, ln2 := listenLoopbackPair(t)
	serveHTTPOn(t, ln1, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	serveHTTPOn(t, ln2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ip1 := ln1.Addr().(*net.TCPAddr).IP.String()
	ip2 := ln2.Addr().(*net.TCPAddr).IP.String()
	port := ln1.Addr().(*net.TCPAddr).Port

	var lookups atomic.Int32
	origLookup := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		lookups.Add(1)
		return []string{ip1, ip2}, nil
	}
	t.Cleanup(func() { netLookupHost = origLookup })

	origTTL := loadBalanceDNSCacheTTL
	loadBalanceDNSCacheTTL = time.Hour
	t.Cleanup(func() { loadBalanceDNSCacheTTL = origTTL })

	origChoose := chooseIPFromList
	chooseIPFromList = func(IPs []string) string { return IPs[0] }
	t.Cleanup(func() { chooseIPFromList = origChoose })

	client := NewClient().EnableLoadBalanceRandom(true)
	target := "http://" + net.JoinHostPort(lbTestHost, strconv.Itoa(port)) + "/"
	require.NoError(t, client.Get(context.Background(), target, nil))
	require.NoError(t, client.Get(context.Background(), target, nil))
	assert.Equal(t, int32(1), lookups.Load())
}

func TestLoadBalanceRandom_DoEarlyOutsLeaveRequestUnchanged(t *testing.T) {
	origLookup := netLookupHost
	t.Cleanup(func() { netLookupHost = origLookup })

	t.Run("flag off", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com/resource", nil)
		require.NoError(t, err)
		origHost, origReqHost := req.URL.Host, req.Host
		out := NewClient().setLoadBalanceTarget(req, "http://example.com/resource", req.URL.Hostname())
		assert.Equal(t, "http://example.com/resource", out)
		assert.Equal(t, origHost, req.URL.Host)
		assert.Equal(t, origReqHost, req.Host)
	})

	t.Run("host is IP", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/resource", nil)
		require.NoError(t, err)
		origHost, origReqHost := req.URL.Host, req.Host
		out := NewClient().EnableLoadBalanceRandom(true).setLoadBalanceTarget(req, "http://127.0.0.1/resource", req.URL.Hostname())
		assert.Equal(t, "http://127.0.0.1/resource", out)
		assert.Equal(t, origHost, req.URL.Host)
		assert.Equal(t, origReqHost, req.Host)
	})

	t.Run("resolve error", func(t *testing.T) {
		netLookupHost = func(ctx context.Context, host string) ([]string, error) {
			return nil, assert.AnError
		}
		req, err := http.NewRequest(http.MethodGet, "http://example.com/resource", nil)
		require.NoError(t, err)
		origHost, origReqHost := req.URL.Host, req.Host
		out := NewClient().EnableLoadBalanceRandom(true).setLoadBalanceTarget(req, "http://example.com/resource", req.URL.Hostname())
		assert.Equal(t, "http://example.com/resource", out)
		assert.Equal(t, origHost, req.URL.Host)
		assert.Equal(t, origReqHost, req.Host)
	})

	t.Run("single address", func(t *testing.T) {
		netLookupHost = func(ctx context.Context, host string) ([]string, error) {
			return []string{"192.0.2.1"}, nil
		}
		req, err := http.NewRequest(http.MethodGet, "http://example.com/resource", nil)
		require.NoError(t, err)
		origHost, origReqHost := req.URL.Host, req.Host
		out := NewClient().EnableLoadBalanceRandom(true).setLoadBalanceTarget(req, "http://example.com/resource", req.URL.Hostname())
		assert.Equal(t, "http://example.com/resource", out)
		assert.Equal(t, origHost, req.URL.Host)
		assert.Equal(t, origReqHost, req.Host)
	})
}

func listenLoopbackPair(t *testing.T) (net.Listener, net.Listener) {
	t.Helper()
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln1.Addr().(*net.TCPAddr).Port
	ln2, err := net.Listen("tcp", net.JoinHostPort("127.0.0.2", strconv.Itoa(port)))
	if err != nil {
		_ = ln1.Close()
		t.Skipf("cannot bind 127.0.0.2:%d: %v", port, err)
	}
	return ln1, ln2
}

func serveHTTPOn(t *testing.T, ln net.Listener, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func patchLookupHost(t *testing.T, ips ...string) {
	t.Helper()
	orig := netLookupHost
	netLookupHost = func(ctx context.Context, host string) ([]string, error) {
		return ips, nil
	}
	t.Cleanup(func() { netLookupHost = orig })
}

func mustDNSSANOnlyCert(t *testing.T, dnsName string) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lb-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: mustParseCert(t, der)}, pool
}

func mustParseCert(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}
