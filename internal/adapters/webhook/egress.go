package webhook

import (
	"bufio"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// proxyHandshake bounds asking the proxy for a tunnel, on top of reaching it.
const proxyHandshake = 10 * time.Second

// ErrProxy is the outbound proxy failing to give a tunnel: down, refusing, or asking for
// a password it was not given.
var ErrProxy = errors.New("the outbound proxy")

// Dialer dials as the guard allows (D428). With no proxy, each address is checked as it is
// dialled, after DNS. Through the proxy, a public address is resolved and checked here
// first, and the proxy asked for a tunnel to that address alone, so no name resolves its
// way inside at the proxy either; TLS stays end to end. A private one, where allowed, is
// dialled directly: the proxy is for the internet.
type Dialer struct {
	direct       *net.Dialer
	allowPrivate func() bool
	proxy        func() string
	resolver     *net.Resolver
	public       func(netip.Addr) bool // public, but for tests
}

// DialContext matches http.Transport's.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	raw := ""
	if d.proxy != nil {
		raw = d.proxy()
	}

	if raw == "" {
		return d.direct.DialContext(ctx, network, address)
	}

	via, err := ParseProxy(raw)
	if err != nil {
		return nil, err
	}

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("port %q: %w", portText, err)
	}

	addrs, err := d.lookup(ctx, host)
	if err != nil {
		return nil, err
	}

	// IPv4 first: a proxy without a route for IPv6 would hang on each such address.
	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		return cmp.Compare(family(a), family(b))
	})

	var last error

	for _, a := range addrs {
		target := netip.AddrPortFrom(a.Unmap(), uint16(port))

		var conn net.Conn

		switch {
		case d.public(target.Addr()):
			conn, err = d.viaProxy(ctx, via, target)
		case d.allowPrivate != nil && d.allowPrivate():
			conn, err = d.direct.DialContext(ctx, network, target.String())
		default:
			err = fmt.Errorf("%w: %s", ErrForbiddenAddress, target.Addr())
		}

		if err == nil {
			return conn, nil
		}

		last = err
	}

	return nil, last
}

// family orders addresses IPv4 first.
func family(a netip.Addr) int {
	if a.Unmap().Is4() {
		return 0
	}

	return 1
}

func (d *Dialer) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}

	addrs, err := d.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("no address for %s", host)
	}

	return addrs, nil
}

// viaProxy is a connection to target through the proxy.
func (d *Dialer) viaProxy(ctx context.Context, via *url.URL, target netip.AddrPort) (net.Conn, error) {
	// The proxy is the admin's, wherever it is: not the guard's to check.
	forward := &net.Dialer{Timeout: d.direct.Timeout}

	if via.Scheme == "socks5" {
		var auth *proxy.Auth
		if via.User != nil {
			pass, _ := via.User.Password()
			auth = &proxy.Auth{User: via.User.Username(), Password: pass}
		}

		socks, err := proxy.SOCKS5("tcp", via.Host, auth, forward)
		if err != nil {
			return nil, fmt.Errorf("%w at %s: %w", ErrProxy, via.Host, err)
		}

		conn, err := socks.(proxy.ContextDialer).DialContext(ctx, "tcp", target.String())
		if err != nil {
			return nil, fmt.Errorf("%w at %s: %w", ErrProxy, via.Host, err)
		}

		return conn, nil
	}

	conn, err := forward.DialContext(ctx, "tcp", via.Host)
	if err != nil {
		return nil, fmt.Errorf("%w at %s: %w", ErrProxy, via.Host, err)
	}

	if err := connect(ctx, conn, via, target); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return conn, nil
}

// connect asks an HTTP proxy for a tunnel to target over conn, with CONNECT.
func connect(ctx context.Context, conn net.Conn, via *url.URL, target netip.AddrPort) error {
	deadline := time.Now().Add(proxyHandshake)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	_ = conn.SetDeadline(deadline)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target.String()},
		Host:   target.String(),
		Header: http.Header{},
	}

	if via.User != nil {
		pass, _ := via.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(via.User.Username()+":"+pass)))
	}

	if err := req.Write(conn); err != nil {
		return fmt.Errorf("%w at %s: %w", ErrProxy, via.Host, err)
	}

	// One byte at a time, so nothing of the tunnel is read past the proxy's answer.
	res, err := http.ReadResponse(bufio.NewReaderSize(oneByte{conn}, 1), req)
	if err != nil {
		return fmt.Errorf("%w at %s: %w", ErrProxy, via.Host, err)
	}

	_ = res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w at %s refused the tunnel: %s", ErrProxy, via.Host, res.Status)
	}

	return nil
}

// oneByte reads at most a byte at a time.
type oneByte struct{ net.Conn }

func (o oneByte) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}

	return o.Conn.Read(p)
}

// ParseProxy reads an outbound proxy's URL: http:// or socks5://, a host and a port, a
// user and a password before them if it asks for them, nothing after.
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)

	switch {
	case err != nil, u.Scheme != "http" && u.Scheme != "socks5", u.Hostname() == "", u.Port() == "",
		u.Path != "" && u.Path != "/", u.RawQuery != "", u.Fragment != "":
		return nil, errors.New("is not a proxy's address: http://host:port or socks5://host:port, a user:password@ before the host if it asks")
	}

	return u, nil
}
