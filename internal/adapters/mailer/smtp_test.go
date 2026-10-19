package mailer

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// fakeServer is an SMTP server for tests: PLAIN auth, STARTTLS when it has a certificate,
// TLS from the start when implicit, and refusing whom it is told to.
type fakeServer struct {
	addr     string
	cert     *tls.Certificate
	implicit bool
	user     string
	pass     string
	refuse   string // a recipient answered 550

	mu       sync.Mutex
	data     []string
	authed   []string
	tlsAfter bool
}

func (f *fakeServer) start(t *testing.T) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	if f.implicit {
		l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{*f.cert}})
	}

	f.addr = l.Addr().String()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go f.serve(conn)
		}
	}()
}

func (f *fakeServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	r, w := bufio.NewReader(conn), conn
	say := func(line string) { _, _ = w.Write([]byte(line + "\r\n")) }
	secure := f.implicit

	say("220 fake ESMTP")

	for {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		cmd := strings.TrimSpace(line)
		upper := strings.ToUpper(cmd)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			say("250-fake")
			if f.cert != nil && !secure {
				say("250-STARTTLS")
			}
			say("250 AUTH PLAIN")
		case upper == "STARTTLS":
			say("220 go ahead")

			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*f.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}

			conn, r, w, secure = tc, bufio.NewReader(tc), tc, true

			f.mu.Lock()
			f.tlsAfter = true
			f.mu.Unlock()
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")

			f.mu.Lock()
			f.authed = append(f.authed, strings.Join(parts[1:], ":"))
			f.mu.Unlock()

			if len(parts) == 3 && parts[1] == f.user && parts[2] == f.pass {
				say("235 2.7.0 Authentication successful")
			} else {
				say("535 5.7.8 Authentication credentials invalid")
			}
		case strings.HasPrefix(upper, "MAIL FROM"):
			say("250 2.1.0 Ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			if f.refuse != "" && strings.Contains(cmd, f.refuse) {
				say("550 5.1.1 No such user here")
			} else {
				say("250 2.1.5 Ok")
			}
		case upper == "DATA":
			say("354 End data with <CR><LF>.<CR><LF>")

			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}

				if l == ".\r\n" {
					break
				}

				b.WriteString(l)
			}

			f.mu.Lock()
			f.data = append(f.data, b.String())
			f.mu.Unlock()

			say("250 2.0.0 Ok: queued")
		case upper == "QUIT":
			say("221 2.0.0 Bye")
			return
		default: // NOOP, RSET
			say("250 2.0.0 Ok")
		}
	}
}

func (f *fakeServer) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.data...)
}

// selfSigned is a certificate for 127.0.0.1, and a pool trusting it.
func selfSigned(t *testing.T) (*tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

func mailerFor(t *testing.T, f *fakeServer, security string, trust *x509.CertPool, user, pass string) *SMTP {
	t.Helper()

	host, port, err := net.SplitHostPort(f.addr)
	require.NoError(t, err)
	n, _ := strconv.Atoi(port)

	s := New(func() Config {
		return Config{Host: host, Port: n, Security: security, Username: user, Password: pass,
			From: "polythought@example.com", FromName: "Polythought"}
	}, logging.Noop())
	s.rootCAs, s.timeout = trust, 5*time.Second

	return s
}

var hello = ports.Mail{To: "ada@example.com", Subject: "Hello", Text: "Plain words.", HTML: "<p>HTML words.</p>"}

func TestSendPlain(t *testing.T) {
	f := &fakeServer{}
	f.start(t)

	require.NoError(t, mailerFor(t, f, Plain, nil, "", "").Send(context.Background(), hello))

	msgs := f.messages()
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `From: "Polythought" <polythought@example.com>`)
	assert.Contains(t, msgs[0], "To: <ada@example.com>")
	assert.Contains(t, msgs[0], "Subject: Hello")
	assert.Contains(t, msgs[0], "multipart/alternative")
	assert.Contains(t, msgs[0], "text/plain")
	assert.Contains(t, msgs[0], "Plain words.")
	assert.Contains(t, msgs[0], "text/html")
	assert.Regexp(t, `Message-ID: <[0-9a-f]{32}@example\.com>`, msgs[0], "on the From domain, naming no machine")
	assert.Contains(t, msgs[0], "Auto-Submitted: auto-generated")
}

func TestSendSignsIn(t *testing.T) {
	f := &fakeServer{user: "poly", pass: "s3cret"}
	f.start(t)

	// To this host, a plain connection may carry the password.
	require.NoError(t, mailerFor(t, f, Plain, nil, "poly", "s3cret").Send(context.Background(), hello))
	assert.Len(t, f.messages(), 1)

	err := mailerFor(t, f, Plain, nil, "poly", "wrong").Send(context.Background(), hello)
	require.ErrorIs(t, err, domain.ErrMailFailed)
	assert.Contains(t, err.Error(), "535", "what the server answered")
	assert.NotContains(t, err.Error(), "wrong", "never the password")
}

func TestSendRefused(t *testing.T) {
	f := &fakeServer{refuse: "nobody@example.com"}
	f.start(t)

	m := hello
	m.To = "nobody@example.com"

	err := mailerFor(t, f, Plain, nil, "", "").Send(context.Background(), m)
	require.ErrorIs(t, err, domain.ErrMailFailed)
	assert.Contains(t, err.Error(), "550")
	assert.Contains(t, err.Error(), "No such user here")
}

func TestSendSTARTTLS(t *testing.T) {
	cert, pool := selfSigned(t)
	f := &fakeServer{cert: cert, user: "poly", pass: "s3cret"}
	f.start(t)

	t.Run("an untrusted certificate is refused", func(t *testing.T) {
		err := mailerFor(t, f, STARTTLS, nil, "poly", "s3cret").Send(context.Background(), hello)
		require.ErrorIs(t, err, domain.ErrMailFailed)
		assert.Contains(t, err.Error(), "certificate")
		assert.Empty(t, f.messages())
	})

	t.Run("a trusted one is used", func(t *testing.T) {
		require.NoError(t, mailerFor(t, f, STARTTLS, pool, "poly", "s3cret").Send(context.Background(), hello))
		assert.Len(t, f.messages(), 1)
		assert.True(t, f.tlsAfter)
	})
}

func TestSendSTARTTLSRequired(t *testing.T) {
	f := &fakeServer{} // offers no STARTTLS
	f.start(t)

	err := mailerFor(t, f, STARTTLS, nil, "", "").Send(context.Background(), hello)
	require.ErrorIs(t, err, domain.ErrMailFailed)
	assert.Empty(t, f.messages(), "nothing goes in the clear")
}

func TestSendTLS(t *testing.T) {
	cert, pool := selfSigned(t)
	f := &fakeServer{cert: cert, implicit: true}
	f.start(t)

	err := mailerFor(t, f, TLS, nil, "", "").Send(context.Background(), hello)
	require.ErrorIs(t, err, domain.ErrMailFailed, "untrusted")

	require.NoError(t, mailerFor(t, f, TLS, pool, "", "").Send(context.Background(), hello))
	assert.Len(t, f.messages(), 1)
}

func TestNotSetUp(t *testing.T) {
	s := New(func() Config { return Config{Host: "mail.example.com"} }, logging.Noop())
	assert.False(t, s.SetUp(), "no From address")
	require.ErrorIs(t, s.Send(context.Background(), hello), domain.ErrMailNotSetUp)

	s = New(func() Config { return Config{Host: "mail.example.com", From: "p@example.com"} }, logging.Noop())
	assert.True(t, s.SetUp())
}

func TestSendToNoAddress(t *testing.T) {
	f := &fakeServer{}
	f.start(t)

	m := hello
	m.To = "not an address"

	require.ErrorIs(t, mailerFor(t, f, Plain, nil, "", "").Send(context.Background(), m), domain.ErrInvalidEmail)
}
