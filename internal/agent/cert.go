package agent

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"
)

// parseCertExpiry extracts the NotAfter time from a PEM-encoded certificate.
func parseCertExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing certificate: %w", err)
	}

	return cert.NotAfter, nil
}
