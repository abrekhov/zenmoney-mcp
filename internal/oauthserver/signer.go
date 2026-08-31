package oauthserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errInvalidToken = errors.New("invalid token")

type signer struct{ key []byte }

func newSigner(key string) (*signer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("MCP_SIGNING_KEY must contain at least 32 characters")
	}
	return &signer{key: []byte(key)}, nil
}

func (s *signer) sign(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "v1." + encoded + "." + signature, nil
}

func (s *signer) verify(token string, output any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return errInvalidToken
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(parts[1]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expected, actual) {
		return errInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, output) != nil {
		return errInvalidToken
	}
	return nil
}

type baseClaims struct {
	Type string `json:"typ"`
	IAT  int64  `json:"iat"`
	EXP  int64  `json:"exp"`
}

func (c baseClaims) valid(wantType string, now time.Time) bool {
	return c.Type == wantType && c.IAT <= now.Add(time.Minute).Unix() && c.EXP > now.Unix()
}

type clientClaims struct {
	baseClaims
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name,omitempty"`
}

type authorizationClaims struct {
	baseClaims
	ClientID      string   `json:"client_id"`
	RedirectURI   string   `json:"redirect_uri"`
	State         string   `json:"state,omitempty"`
	Scopes        []string `json:"scope"`
	CodeChallenge string   `json:"code_challenge"`
	Nonce         string   `json:"nonce"`
}

type accessClaims struct {
	baseClaims
	Subject  string   `json:"sub"`
	Audience string   `json:"aud"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scope"`
	Nonce    string   `json:"nonce"`
}
