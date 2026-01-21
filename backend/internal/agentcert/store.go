package agentcert

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

var ErrRequestNotFound = errors.New("cert request not found or expired")

func CreateRequest(db *sql.DB, serverID string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	if serverID == "" {
		return "", errors.New("serverID is required")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}

	expiresAt := time.Now().Add(ttl)
	_, err = db.Exec(`
		INSERT INTO agent_cert_requests (token, server_id, expires_at)
		VALUES (?, ?, ?)
	`, token, serverID, expiresAt)
	if err != nil {
		return "", fmt.Errorf("insert cert request: %w", err)
	}

	return token, nil
}

func ConsumeRequest(tx *sql.Tx, token, hostUUID, usedByIP string) (string, error) {
	if token == "" {
		return "", ErrRequestNotFound
	}

	row := tx.QueryRow(`
		SELECT server_id
		FROM agent_cert_requests
		WHERE token = ? AND used_at IS NULL AND expires_at > datetime('now')
	`, token)

	var serverID string
	if err := row.Scan(&serverID); err != nil {
		return "", ErrRequestNotFound
	}

	_, err := tx.Exec(`
		UPDATE agent_cert_requests
		SET used_at = datetime('now'), host_uuid = ?, used_by_ip = ?
		WHERE token = ?
	`, hostUUID, usedByIP, token)
	if err != nil {
		return "", fmt.Errorf("mark cert request used: %w", err)
	}

	return serverID, nil
}

func InsertCertificate(tx *sql.Tx, serverID, hostUUID, serial, fingerprint string, certPEM []byte, expiresAt time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO agent_certificates (server_id, host_uuid, serial, fingerprint, cert_pem, issued_at, expires_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?)
	`, serverID, hostUUID, serial, fingerprint, string(certPEM), expiresAt)
	if err != nil {
		return fmt.Errorf("insert agent cert: %w", err)
	}
	return nil
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
