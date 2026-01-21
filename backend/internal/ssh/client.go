package ssh

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Client wraps an SSH connection
type Client struct {
	config       *ClientConfig
	client       *ssh.Client
	connectedAt  time.Time
	lastActivity time.Time
}

// ClientConfig holds SSH connection configuration
type ClientConfig struct {
	Host            string
	Port            int
	Username        string
	AuthMethod      string // "key" or "password"
	KeyPath         string
	Password        string
	Timeout         time.Duration
	KnownHostsPath  string
	TrustOnFirstUse bool
}

// NewClient creates a new SSH client
func NewClient(config *ClientConfig) (*Client, error) {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	client := &Client{
		config: config,
	}

	if err := client.Connect(); err != nil {
		return nil, err
	}

	return client, nil
}

// Connect establishes the SSH connection
func (c *Client) Connect() error {
	var authMethod ssh.AuthMethod

	switch c.config.AuthMethod {
	case "key":
		key, err := c.loadPrivateKey(c.config.KeyPath)
		if err != nil {
			return fmt.Errorf("failed to load private key: %w", err)
		}
		authMethod = ssh.PublicKeys(key)

	case "password":
		authMethod = ssh.Password(c.config.Password)

	default:
		return fmt.Errorf("unsupported auth method: %s", c.config.AuthMethod)
	}

	hostKeyCallback, err := NewHostKeyCallback(c.config.KnownHostsPath, c.config.TrustOnFirstUse)
	if err != nil {
		return fmt.Errorf("failed to configure host key verification: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            c.config.Username,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: hostKeyCallback,
		Timeout:         c.config.Timeout,
	}

	address := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to dial SSH: %w", err)
	}

	c.client = client
	c.connectedAt = time.Now()
	c.lastActivity = time.Now()

	return nil
}

// Close closes the SSH connection
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// IsConnected checks if the connection is still active
func (c *Client) IsConnected() bool {
	if c.client == nil {
		return false
	}

	// Try to send a keepalive
	_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
	if err != nil {
		return false
	}

	c.lastActivity = time.Now()
	return true
}

// RunCommand executes a command and returns the output
func (c *Client) RunCommand(command string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(command)
	c.lastActivity = time.Now()

	if err != nil {
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	return string(output), nil
}

// RunCommandWithPTY executes a command with a PTY of the requested size.
func (c *Client) RunCommandWithPTY(command string, cols, rows int) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		return "", fmt.Errorf("request for pseudo terminal failed: %w", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	if err := session.Run(command); err != nil {
		output := stdout.String() + stderr.String()
		return output, fmt.Errorf("command failed: %w", err)
	}

	c.lastActivity = time.Now()
	return stdout.String() + stderr.String(), nil
}

// RunCommandWithTimeout executes a command with a timeout
func (c *Client) RunCommandWithTimeout(command string, timeout time.Duration) (string, error) {
	type result struct {
		output string
		err    error
	}

	resultChan := make(chan result, 1)

	go func() {
		output, err := c.RunCommand(command)
		resultChan <- result{output, err}
	}()

	select {
	case res := <-resultChan:
		return res.output, res.err
	case <-time.After(timeout):
		return "", fmt.Errorf("command timed out after %v", timeout)
	}
}

// StartInteractiveSession creates a new session for interactive use (PTY)
func (c *Client) StartInteractiveSession() (*ssh.Session, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Request PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	if err := session.RequestPty("xterm", 80, 40, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request for pseudo terminal failed: %w", err)
	}

	c.lastActivity = time.Now()
	return session, nil
}

// StreamCommand runs a command and streams output to the provided writer
func (c *Client) StreamCommand(command string, stdout, stderr io.Writer) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr

	if err := session.Run(command); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}

	c.lastActivity = time.Now()
	return nil
}

// GetUptime returns how long the connection has been active
func (c *Client) GetUptime() time.Duration {
	return time.Since(c.connectedAt)
}

// GetLastActivity returns when the connection was last used
func (c *Client) GetLastActivity() time.Time {
	return c.lastActivity
}

// NewSession creates a new SSH session
func (c *Client) NewSession() (*ssh.Session, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	return c.client.NewSession()
}

// NewSFTP creates a new SFTP client
func (c *Client) NewSFTP() (*sftp.Client, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	c.lastActivity = time.Now()
	return sftp.NewClient(c.client)
}

// NewSFTPWithOptions creates a new SFTP client with options
func (c *Client) NewSFTPWithOptions(opts ...sftp.ClientOption) (*sftp.Client, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	c.lastActivity = time.Now()
	return sftp.NewClient(c.client, opts...)
}

// GetConfig returns the client configuration
func (c *Client) GetConfig() *ClientConfig {
	return c.config
}

// loadPrivateKey loads an SSH private key from a file
func (c *Client) loadPrivateKey(path string) (ssh.Signer, error) {
	key, err := ReadPrivateKeyBytes(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}

	// Parse the private key
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %w", err)
	}

	return signer, nil
}

// TestConnection tests if the connection is working
func (c *Client) TestConnection() error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected")
	}

	// Try a simple command
	_, err := c.RunCommandWithTimeout("echo 'test'", 5*time.Second)
	return err
}

// GetLocalAddr returns the local address of the connection
func (c *Client) GetLocalAddr() net.Addr {
	if c.client != nil && c.client.Conn != nil {
		return c.client.Conn.LocalAddr()
	}
	return nil
}

// GetRemoteAddr returns the remote address of the connection
func (c *Client) GetRemoteAddr() net.Addr {
	if c.client != nil && c.client.Conn != nil {
		return c.client.Conn.RemoteAddr()
	}
	return nil
}
