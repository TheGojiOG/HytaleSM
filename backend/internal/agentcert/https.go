package agentcert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

func IssueSelfSignedServerCert(host, serverID, hostUUID string, ttl time.Duration) (certPEM, keyPEM []byte, serial string, notAfter time.Time, fingerprint string, err error) {
	if ttl == 0 {
		ttl = 365 * 24 * time.Hour
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("serial: %w", err)
	}

	cn := serverID
	if cn == "" {
		cn = hostUUID
	}
	cn = strings.TrimSpace(cn)
	if cn == "" {
		cn = "hytale-agent"
	}

	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"Hytale Manager"},
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	host = strings.TrimSpace(host)
	if host != "" {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = []net.IP{ip}
		} else {
			tmpl.DNSNames = []string{host}
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("create cert: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	serial = fmt.Sprintf("%x", serialNumber)

	h := sha256.Sum256(der)
	fingerprint = fmt.Sprintf("%x", h[:])

	return certPEM, keyPEM, serial, tmpl.NotAfter, fingerprint, nil
}

func IssueServerCert(ca *CA, host, serverID, hostUUID string, ttl time.Duration) (certPEM, keyPEM []byte, serial string, notAfter time.Time, fingerprint string, err error) {
	if ca == nil || ca.Cert == nil || ca.Key == nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("invalid CA")
	}
	if ttl == 0 {
		ttl = 365 * 24 * time.Hour
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("serial: %w", err)
	}

	cn := serverID
	if cn == "" {
		cn = hostUUID
	}
	cn = strings.TrimSpace(cn)
	if cn == "" {
		cn = "hytale-agent"
	}

	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"Hytale Manager"},
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	host = strings.TrimSpace(host)
	if host != "" {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = []net.IP{ip}
		} else {
			tmpl.DNSNames = []string{host}
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("create cert: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	serial = fmt.Sprintf("%x", serialNumber)

	h := sha256.Sum256(der)
	fingerprint = fmt.Sprintf("%x", h[:])

	return certPEM, keyPEM, serial, tmpl.NotAfter, fingerprint, nil
}

func IssueClientCert(ca *CA, name string, ttl time.Duration) (certPEM, keyPEM []byte, serial string, notAfter time.Time, fingerprint string, err error) {
	if ca == nil || ca.Cert == nil || ca.Key == nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("invalid CA")
	}
	if ttl == 0 {
		ttl = 365 * 24 * time.Hour
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("serial: %w", err)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = "server-manager"
	}

	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"Hytale Manager"},
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(ttl),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, "", time.Time{}, "", fmt.Errorf("create cert: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	serial = fmt.Sprintf("%x", serialNumber)

	h := sha256.Sum256(der)
	fingerprint = fmt.Sprintf("%x", h[:])

	return certPEM, keyPEM, serial, tmpl.NotAfter, fingerprint, nil
}
