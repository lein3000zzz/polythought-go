package webhook

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProxy is an outbound proxy that keeps what it was asked for.
type fakeProxy struct {
	addr string

	mu      sync.Mutex
	targets []string // as asked: an address, never a name, from the guard
	auth    []string // what came as its credentials
}

func (p *fakeProxy) asked(target, auth string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.targets, p.auth = append(p.targets, target), append(p.auth, auth)
}

func (p *fakeProxy) seen() ([]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.targets...), append([]string(nil), p.auth...)
}

func listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			go serve(c)
		}
	}()

	return ln.Addr().String()
}

// pipe joins the client to target, both ways, until either closes.
func pipe(client net.Conn, target string) {
	defer func() { _ = client.Close() }()

	up, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = up.Close() }()

	go func() { _, _ = io.Copy(up, client) }()

	_, _ = io.Copy(client, up)
}

// httpProxy answers CONNECT, refusing with refuse unless it is 0.
func httpProxy(t *testing.T, refuse int) *fakeProxy {
	t.Helper()

	p := &fakeProxy{}
	p.addr = listen(t, func(c net.Conn) {
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil || req.Method != http.MethodConnect {
			_ = c.Close()
			return
		}

		p.asked(req.Host, req.Header.Get("Proxy-Authorization"))

		if refuse != 0 {
			_, _ = io.WriteString(c, "HTTP/1.1 "+strconv.Itoa(refuse)+" "+http.StatusText(refuse)+"\r\n\r\n")
			_ = c.Close()

			return
		}

		up, err := net.Dial("tcp", req.Host)
		if err != nil {
			_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			_ = c.Close()

			return
		}

		_ = up.Close()
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		pipe(c, req.Host)
	})

	return p
}

// socksProxy speaks enough SOCKS5 for a CONNECT, with a user and a password.
func socksProxy(t *testing.T) *fakeProxy {
	t.Helper()

	p := &fakeProxy{}
	p.addr = listen(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		head := make([]byte, 2)
		if _, err := io.ReadFull(r, head); err != nil || head[0] != 5 {
			_ = c.Close()
			return
		}

		methods := make([]byte, head[1])
		_, _ = io.ReadFull(r, methods)
		_, _ = c.Write([]byte{5, 2}) // user and password

		ver := make([]byte, 2)
		_, _ = io.ReadFull(r, ver)
		user := make([]byte, ver[1])
		_, _ = io.ReadFull(r, user)
		n, _ := r.ReadByte()
		pass := make([]byte, n)
		_, _ = io.ReadFull(r, pass)
		_, _ = c.Write([]byte{1, 0})

		req := make([]byte, 4)
		_, _ = io.ReadFull(r, req)

		var host string

		switch req[3] {
		case 1:
			ip := make([]byte, 4)
			_, _ = io.ReadFull(r, ip)
			host = netip.AddrFrom4([4]byte(ip)).String()
		case 4:
			ip := make([]byte, 16)
			_, _ = io.ReadFull(r, ip)
			host = netip.AddrFrom16([16]byte(ip)).String()
		default: // a name: the guard never sends one
			n, _ := r.ReadByte()
			name := make([]byte, n)
			_, _ = io.ReadFull(r, name)
			host = "name:" + string(name)
		}

		port := make([]byte, 2)
		_, _ = io.ReadFull(r, port)
		target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
		p.asked(target, string(user)+":"+string(pass))

		// As a proxy does: it connects before it says so.
		up, err := net.Dial("tcp", target)
		if err != nil {
			_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
			_ = c.Close()

			return
		}

		_ = up.Close()
		_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		pipe(c, target)
	})

	return p
}

// target is a server standing for a public one: the dialers below take loopback as public.
func target(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	return srv, port
}

func get(t *testing.T, d *Dialer, url string) (string, error) {
	t.Helper()

	client := &http.Client{Transport: &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true}}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()

	b, err := io.ReadAll(res.Body)

	return string(b), err
}

func loopbackIsPublic(d *Dialer) *Dialer {
	d.public = func(a netip.Addr) bool { return a.IsLoopback() }
	return d
}

func TestProxy(t *testing.T) {
	_, port := target(t)
	off := func() bool { return false }

	for name, start := range map[string]func(*testing.T) (*fakeProxy, string){
		"http": func(t *testing.T) (*fakeProxy, string) {
			p := httpProxy(t, 0)
			return p, "http://alice:s3cret@" + p.addr
		},
		"socks5": func(t *testing.T) (*fakeProxy, string) {
			p := socksProxy(t)
			return p, "socks5://alice:s3cret@" + p.addr
		},
	} {
		t.Run(name+": a public address goes through it, the proxy told the address, not the name", func(t *testing.T) {
			p, raw := start(t)
			d := loopbackIsPublic(GuardedDialer(off, func() string { return raw }))

			body, err := get(t, d, "http://localhost:"+port+"/")
			require.NoError(t, err)
			assert.Equal(t, "ok", body)

			targets, auth := p.seen()
			require.NotEmpty(t, targets)
			assert.Equal(t, []string{"127.0.0.1:" + port}, targets, "localhost resolved here, IPv4 first")

			for _, tg := range targets {
				host, _, _ := net.SplitHostPort(tg)
				_, err := netip.ParseAddr(host)
				assert.NoError(t, err, "never a name: %s", tg)
			}

			if name == "http" {
				assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:s3cret")), auth[0])
			} else {
				assert.Equal(t, "alice:s3cret", auth[0])
			}
		})
	}

	t.Run("a private address goes direct, where allowed, past the proxy", func(t *testing.T) {
		p := httpProxy(t, 0)
		d := GuardedDialer(func() bool { return true }, func() string { return "http://" + p.addr })

		body, err := get(t, d, "http://127.0.0.1:"+port+"/")
		require.NoError(t, err)
		assert.Equal(t, "ok", body)

		targets, _ := p.seen()
		assert.Empty(t, targets, "the proxy is for the internet")
	})

	t.Run("a private address, not allowed, goes nowhere", func(t *testing.T) {
		p := httpProxy(t, 0)
		d := GuardedDialer(off, func() string { return "http://" + p.addr })

		_, err := get(t, d, "http://127.0.0.1:"+port+"/")
		require.ErrorIs(t, err, ErrForbiddenAddress)

		targets, _ := p.seen()
		assert.Empty(t, targets)
	})

	t.Run("a proxy refusing says so, and not its password", func(t *testing.T) {
		p := httpProxy(t, http.StatusProxyAuthRequired)
		d := loopbackIsPublic(GuardedDialer(off, func() string { return "http://alice:s3cret@" + p.addr }))

		_, err := get(t, d, "http://127.0.0.1:"+port+"/")
		require.ErrorIs(t, err, ErrProxy)
		assert.Contains(t, err.Error(), "refused the tunnel: 407 Proxy Authentication Required")
		assert.NotContains(t, err.Error(), "s3cret")
	})

	t.Run("no proxy set: as before", func(t *testing.T) {
		d := GuardedDialer(off, func() string { return "" })

		_, err := get(t, d, "http://127.0.0.1:"+port+"/")
		require.ErrorIs(t, err, ErrForbiddenAddress, "the guard on the dial itself")
	})
}

func TestParseProxy(t *testing.T) {
	for _, ok := range []string{"http://192.168.0.13:10809", "socks5://10.0.0.2:1080", "http://u:p@proxy.lan:3128", "http://proxy.lan:3128/"} {
		_, err := ParseProxy(ok)
		assert.NoError(t, err, ok)
	}

	for _, bad := range []string{"192.168.0.13:10809", "https://proxy:443", "http://proxy", "http://proxy:3128/path", "http://proxy:3128?x=1", "socks4://proxy:1080", "http://:3128"} {
		_, err := ParseProxy(bad)
		require.Error(t, err, bad)
		assert.NotContains(t, err.Error(), bad, "the value is not quoted back: it may hold a password")
	}
}
