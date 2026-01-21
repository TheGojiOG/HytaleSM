package agentcert

import (
	"database/sql"
	"fmt"
	"time"
)

func InsertHTTPSCertificate(tx *sql.Tx, serverID, hostUUID, serial, fingerprint string, certPEM, keyPEM []byte, expiresAt time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO agent_https_certs (server_id, host_uuid, serial, fingerprint, cert_pem, key_pem, issued_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), ?)
	`, serverID, hostUUID, serial, fingerprint, string(certPEM), string(keyPEM), expiresAt)
	if err != nil {
		return fmt.Errorf("insert https cert: %w", err)
	}
	return nil
}

type ClientCert struct {
	Name        string
	CertPEM     []byte
	KeyPEM      []byte
	Serial      string
	Fingerprint string
	ExpiresAt   time.Time
}

func GetClientCert(db *sql.DB, name string) (*ClientCert, error) {
	row := db.QueryRow(`
		SELECT name, cert_pem, key_pem, serial, fingerprint, expires_at
		FROM agent_client_certs
		WHERE name = ? AND revoked_at IS NULL
	`, name)

	var cert ClientCert
	var certPEM string
	var keyPEM string
	if err := row.Scan(&cert.Name, &certPEM, &keyPEM, &cert.Serial, &cert.Fingerprint, &cert.ExpiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	cert.CertPEM = []byte(certPEM)
	cert.KeyPEM = []byte(keyPEM)
	return &cert, nil
}

func InsertClientCert(tx *sql.Tx, name, serial, fingerprint string, certPEM, keyPEM []byte, expiresAt time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO agent_client_certs (name, serial, fingerprint, cert_pem, key_pem, issued_at, expires_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?)
	`, name, serial, fingerprint, string(certPEM), string(keyPEM), expiresAt)
	if err != nil {
		return fmt.Errorf("insert client cert: %w", err)
	}
	return nil
}
