package polymarket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// L2 header names, as the CLOB expects them.
const (
	headerAddress    = "POLY_ADDRESS"
	headerSignature  = "POLY_SIGNATURE"
	headerTimestamp  = "POLY_TIMESTAMP"
	headerAPIKey     = "POLY_API_KEY"
	headerPassphrase = "POLY_PASSPHRASE"
)

var ErrInvalidSecret = errors.New("polymarket: api secret is not base64url")

// L2Credentials authenticate CLOB requests for one Polymarket account. They are passed
// per call, so one client can act for many accounts.
type L2Credentials struct {
	// Address is the signer the API key belongs to (POLY_ADDRESS). For proxy and safe
	// accounts that is the owning EOA, not the proxy wallet.
	Address    string
	APIKey     string
	Secret     string // base64url, as issued
	Passphrase string
}

// Sign computes POLY_SIGNATURE: HMAC-SHA256 keyed with the base64url-decoded secret over
// timestamp + METHOD + path + body, base64url-encoded with padding. path excludes the
// query string; body must be the exact bytes sent.
func (c L2Credentials) Sign(timestamp int64, method, path string, body []byte) (string, error) {
	key, err := decodeSecret(c.Secret)
	if err != nil {
		return "", err
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + method + path))
	mac.Write(body)

	return base64.URLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c L2Credentials) headers(now time.Time, method, path string, body []byte) (http.Header, error) {
	ts := now.Unix()

	sig, err := c.Sign(ts, method, path, body)
	if err != nil {
		return nil, err
	}

	h := http.Header{}
	h.Set(headerAddress, c.Address)
	h.Set(headerSignature, sig)
	h.Set(headerTimestamp, strconv.FormatInt(ts, 10))
	h.Set(headerAPIKey, c.APIKey)
	h.Set(headerPassphrase, c.Passphrase)

	return h, nil
}

// decodeSecret accepts the secret with or without padding.
func decodeSecret(secret string) ([]byte, error) {
	if key, err := base64.URLEncoding.DecodeString(secret); err == nil {
		return key, nil
	}

	if key, err := base64.RawURLEncoding.DecodeString(secret); err == nil {
		return key, nil
	}

	return nil, ErrInvalidSecret
}
