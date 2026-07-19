package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/distribution/distribution/v3/registry/auth/token"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/nickheyer/distroface/internal/settings"
)

// TokenService manages ECDSA keys and signs Docker registry JWTs.
type TokenService struct {
	privateKey *ecdsa.PrivateKey
	cert       *x509.Certificate
	certPEM    []byte
	certPath   string
	keyID      string
	issuer     string
	service    string
	res        *settings.Resolver
}

// ResourceActions matches the Distribution v3 token claim format.
type ResourceActions struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// ClaimSet matches the Distribution v3 token.ClaimSet format.
type ClaimSet struct {
	Issuer     string               `json:"iss"`
	Subject    string               `json:"sub"`
	Audience   josejwt.Audience     `json:"aud"`
	Expiration *josejwt.NumericDate `json:"exp"`
	NotBefore  *josejwt.NumericDate `json:"nbf"`
	IssuedAt   *josejwt.NumericDate `json:"iat"`
	JWTID      string               `json:"jti"`
	Access     []*ResourceActions   `json:"access"`
}

// NewTokenService initializes keys from disk or generates them, then returns a TokenService.
func NewTokenService(dataDir, issuer, service string, res *settings.Resolver) (*TokenService, error) {
	keysDir := filepath.Join(dataDir, "keys")
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create keys directory: %w", err)
	}

	keyPath := filepath.Join(keysDir, "token.key")
	certPath := filepath.Join(keysDir, "token.crt")

	ts := &TokenService{
		certPath: certPath,
		issuer:   issuer,
		service:  service,
		res:      res,
	}

	if err := ts.loadOrGenerate(keyPath, certPath); err != nil {
		return nil, err
	}

	ts.keyID = token.GetRFC7638Thumbprint(ts.privateKey.Public())

	return ts, nil
}

// CertPath returns the path to the certificate PEM file for registry configuration.
func (ts *TokenService) CertPath() string {
	return ts.certPath
}

// Live registry token lifetime
func (ts *TokenService) expiry() time.Duration {
	return time.Duration(ts.res.System(context.Background()).GetAuth().GetTokenExpirySeconds()) * time.Second
}

// SignToken creates a signed JWT for the given subject and access claims.
func (ts *TokenService) SignToken(subject string, access []*ResourceActions) (string, error) {
	now := time.Now().UTC()

	claims := ClaimSet{
		Issuer:     ts.issuer,
		Subject:    subject,
		Audience:   josejwt.Audience{ts.service},
		Expiration: josejwt.NewNumericDate(now.Add(ts.expiry())),
		NotBefore:  josejwt.NewNumericDate(now.Add(-10 * time.Second)),
		IssuedAt:   josejwt.NewNumericDate(now),
		JWTID:      uuid.New().String(),
		Access:     access,
	}

	signerOpts := jose.SignerOptions{}
	signerOpts.WithHeader("kid", ts.keyID)
	signerOpts.WithHeader("x5c", ts.certChain())

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: ts.privateKey},
		&signerOpts,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create signer: %w", err)
	}

	builder := josejwt.Signed(signer).Claims(claims)
	tokenStr, err := builder.Serialize()
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenStr, nil
}

func (ts *TokenService) certChain() [][]byte {
	return [][]byte{ts.cert.Raw}
}

// Checks token signature gives subject for limit keys
func (ts *TokenService) VerifyTokenSubject(raw string) (string, error) {
	tok, err := josejwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		return "", err
	}
	var claims ClaimSet
	if err := tok.Claims(ts.privateKey.Public(), &claims); err != nil {
		return "", err
	}
	return claims.Subject, nil
}

func (ts *TokenService) loadOrGenerate(keyPath, certPath string) error {
	keyData, keyErr := os.ReadFile(keyPath)
	certData, certErr := os.ReadFile(certPath)

	if keyErr == nil && certErr == nil {
		return ts.loadFromPEM(keyData, certData)
	}

	return ts.generateAndSave(keyPath, certPath)
}

func (ts *TokenService) loadFromPEM(keyPEM, certPEM []byte) error {
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("failed to decode private key PEM")
	}

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	ts.privateKey = key
	ts.cert = cert
	ts.certPEM = certPEM
	return nil
}

func (ts *TokenService) generateAndSave(keyPath, certPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate ECDSA key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "distroface-token-signer",
		},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("failed to parse generated certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	ts.privateKey = key
	ts.cert = cert
	ts.certPEM = certPEM
	return nil
}
