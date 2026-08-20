package assetobservation

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

var errTLSNoCertificate = errors.New("tls endpoint returned no peer certificate")

type certificateResult struct {
	NotBefore       time.Time
	ExpiresAt       time.Time
	SourceReference string
	Metadata        map[string]any
}

type certificateProbe interface {
	Probe(context.Context, string, string) (certificateResult, error)
}

type tlsCollector struct {
	dialer  *publicDialer
	now     func() time.Time
	version uint16
}

func newTLSCollector() *tlsCollector {
	return &tlsCollector{dialer: newPublicDialer(6 * time.Second), now: time.Now, version: tls.VersionTLS12}
}

func (c *tlsCollector) Probe(ctx context.Context, host, port string) (certificateResult, error) {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if host == "" || strings.HasPrefix(host, "*.") {
		return certificateResult{}, errors.New("tls probe requires a concrete hostname")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return certificateResult{}, errors.New("invalid tls port")
	}

	rawConn, err := c.dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return certificateResult{}, fmt.Errorf("connect tls endpoint: %w", err)
	}
	defer rawConn.Close()

	conn := tls.Client(rawConn, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // Verification is performed below so expired certificates can still be observed.
		MinVersion:         c.version,
	})
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		return certificateResult{}, fmt.Errorf("tls handshake: %w", err)
	}
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return certificateResult{}, errTLSNoCertificate
	}
	leaf := state.PeerCertificates[0]
	fingerprint := sha256.Sum256(leaf.Raw)
	hostnameValid := leaf.VerifyHostname(host) == nil
	chainValid, chainError := verifyCertificateChain(leaf, state.PeerCertificates[1:], c.now())

	metadata := map[string]any{
		"host":                   host,
		"port":                   portNumber,
		"subject":                leaf.Subject.String(),
		"issuer":                 leaf.Issuer.String(),
		"serial_number":          leaf.SerialNumber.Text(16),
		"sha256_fingerprint":     strings.ToUpper(hex.EncodeToString(fingerprint[:])),
		"dns_names":              leaf.DNSNames,
		"hostname_valid":         hostnameValid,
		"chain_valid":            chainValid,
		"negotiated_tls_version": tlsVersionName(state.Version),
		"peer_certificate_count": len(state.PeerCertificates),
	}
	if chainError != "" {
		metadata["chain_error"] = chainError
	}
	return certificateResult{
		NotBefore:       leaf.NotBefore,
		ExpiresAt:       leaf.NotAfter,
		SourceReference: net.JoinHostPort(host, port),
		Metadata:        metadata,
	}, nil
}

func verifyCertificateChain(leaf *x509.Certificate, peers []*x509.Certificate, now time.Time) (bool, string) {
	intermediates := x509.NewCertPool()
	for _, certificate := range peers {
		intermediates.AddCert(certificate)
	}
	_, err := leaf.Verify(x509.VerifyOptions{Intermediates: intermediates, CurrentTime: now})
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return fmt.Sprintf("0x%04x", version)
	}
}
