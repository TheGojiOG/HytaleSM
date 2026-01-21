package backup

import (
	"fmt"
	"io"
	"log"
	"path"
	"time"

	"github.com/pkg/sftp"
	sshclient "github.com/yourusername/hytale-server-manager/internal/ssh"
	xssh "golang.org/x/crypto/ssh"
)

// SFTPDestination stores backups on a remote SFTP server
type SFTPDestination struct {
	config     *DestinationConfig
	sshClient  *xssh.Client
	sftpClient *sftp.Client
}

// NewSFTPDestination creates a new SFTP destination
func NewSFTPDestination(config *DestinationConfig) (*SFTPDestination, error) {
	dest := &SFTPDestination{
		config: config,
	}

	// Connect on initialization
	if err := dest.connect(); err != nil {
		return nil, err
	}

	return dest, nil
}

// connect establishes SSH and SFTP connections
func (sd *SFTPDestination) connect() error {
	// Build SSH config
	knownHostsPath := sd.config.KnownHostsPath
	if knownHostsPath == "" {
		knownHostsPath = "./data/known_hosts"
	}

	trustOnFirstUse := sd.config.TrustOnFirstUse
	if !trustOnFirstUse {
		trustOnFirstUse = true
	}

	hostKeyCallback, err := sshclient.NewHostKeyCallback(knownHostsPath, trustOnFirstUse)
	if err != nil {
		return fmt.Errorf("failed to configure host key verification: %w", err)
	}

	sshConfig := &xssh.ClientConfig{
		User:            sd.config.SFTPUsername,
		HostKeyCallback: hostKeyCallback,
		Timeout:         30 * time.Second,
	}

	// Add authentication method
	if sd.config.SFTPKeyPath != "" {
		// Key-based auth
		keyData, err := sshclient.ReadPrivateKeyBytes(sd.config.SFTPKeyPath)
		if err != nil {
			return fmt.Errorf("failed to read SSH key: %w", err)
		}

		signer, err := xssh.ParsePrivateKey(keyData)
		if err != nil {
			return fmt.Errorf("failed to parse SSH key: %w", err)
		}

		sshConfig.Auth = []xssh.AuthMethod{xssh.PublicKeys(signer)}
	} else if sd.config.SFTPPassword != "" {
		// Password-based auth
		sshConfig.Auth = []xssh.AuthMethod{xssh.Password(sd.config.SFTPPassword)}
	} else {
		return fmt.Errorf("no authentication method provided for SFTP")
	}

	// Connect to SSH server
	addr := fmt.Sprintf("%s:%d", sd.config.SFTPHost, sd.config.SFTPPort)
	log.Printf("[SFTPDest] Connecting to %s...", addr)

	sshClient, err := xssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to SSH server: %w", err)
	}
	sd.sshClient = sshClient

	// Create SFTP client
	sftpClient, err := sftp.NewClient(sshClient,
		sftp.MaxPacketUnchecked(131072),
		sftp.UseConcurrentWrites(true),
		sftp.MaxConcurrentRequestsPerFile(64),
	)
	if err != nil {
		sshClient.Close()
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	sd.sftpClient = sftpClient

	// Ensure base directory exists
	if err := sd.sftpClient.MkdirAll(sd.config.Path); err != nil {
		sd.Close()
		return fmt.Errorf("failed to create base directory: %w", err)
	}

	log.Printf("[SFTPDest] Connected successfully")
	return nil
}

// Close closes the SFTP and SSH connections
func (sd *SFTPDestination) Close() error {
	if sd.sftpClient != nil {
		sd.sftpClient.Close()
	}
	if sd.sshClient != nil {
		sd.sshClient.Close()
	}
	return nil
}

// Upload uploads a backup file to the SFTP destination
func (sd *SFTPDestination) Upload(filename string, reader io.Reader, sizeBytes int64) error {
	destPath := path.Join(sd.config.Path, filename)
	log.Printf("[SFTPDest] Uploading %s to %s (%d bytes)", filename, destPath, sizeBytes)

	// Create destination file
	file, err := sd.sftpClient.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer file.Close()

	// Copy data
	written, err := io.Copy(file, reader)
	if err != nil {
		sd.sftpClient.Remove(destPath) // Cleanup on error
		return fmt.Errorf("failed to write remote file: %w", err)
	}

	if written != sizeBytes {
		sd.sftpClient.Remove(destPath)
		return fmt.Errorf("size mismatch: expected %d bytes, wrote %d bytes", sizeBytes, written)
	}

	log.Printf("[SFTPDest] Upload complete: %s", filename)
	return nil
}

// Download downloads a backup file from the SFTP destination
func (sd *SFTPDestination) Download(filename string, writer io.Writer) error {
	srcPath := path.Join(sd.config.Path, filename)
	log.Printf("[SFTPDest] Downloading %s from %s", filename, srcPath)

	file, err := sd.sftpClient.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("failed to read remote file: %w", err)
	}

	log.Printf("[SFTPDest] Download complete: %s", filename)
	return nil
}

// Delete removes a backup file from the SFTP destination
func (sd *SFTPDestination) Delete(filename string) error {
	destPath := path.Join(sd.config.Path, filename)
	log.Printf("[SFTPDest] Deleting %s", destPath)

	if err := sd.sftpClient.Remove(destPath); err != nil {
		return fmt.Errorf("failed to delete remote file: %w", err)
	}

	log.Printf("[SFTPDest] Delete complete: %s", filename)
	return nil
}

// List returns all backup files in the SFTP destination
func (sd *SFTPDestination) List() ([]BackupFile, error) {
	entries, err := sd.sftpClient.ReadDir(sd.config.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote directory: %w", err)
	}

	var files []BackupFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		files = append(files, BackupFile{
			Filename:  entry.Name(),
			SizeBytes: entry.Size(),
			CreatedAt: entry.ModTime().Unix(),
		})
	}

	return files, nil
}

// GetType returns the destination type
func (sd *SFTPDestination) GetType() string {
	return "sftp"
}
