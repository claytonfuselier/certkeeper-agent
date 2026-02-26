package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestParseCertExpiry(t *testing.T) {
	// Generate a self-signed test certificate with a known expiry.
	notAfter := time.Date(2027, 6, 15, 12, 0, 0, 0, time.UTC)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     notAfter,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	got, err := parseCertExpiry(certPEM)
	if err != nil {
		t.Fatalf("parseCertExpiry() error: %v", err)
	}

	if !got.Equal(notAfter) {
		t.Errorf("parseCertExpiry() = %v, want %v", got, notAfter)
	}
}

func TestParseCertExpiry_Invalid(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"not PEM", []byte("not a certificate")},
		{"bad PEM content", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCertExpiry(tt.data)
			if err == nil {
				t.Error("parseCertExpiry() expected error for invalid input")
			}
		})
	}
}
