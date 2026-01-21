package ssh

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourusername/hytale-server-manager/internal/logging"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// NewHostKeyCallback builds a TOFU-capable host key callback using a known_hosts file.
func NewHostKeyCallback(knownHostsPath string, trustOnFirstUse bool) (ssh.HostKeyCallback, error) {
	if strings.TrimSpace(knownHostsPath) == "" {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	if err := ensureKnownHostsFile(knownHostsPath); err != nil {
		return nil, err
	}

	baseCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read known_hosts: %w", err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := baseCallback(hostname, remote, key)
		if err == nil {
			return nil
		}

		keyErr, ok := err.(*knownhosts.KeyError)
		if !ok {
			return err
		}

		if len(keyErr.Want) == 0 {
			if !trustOnFirstUse {
				return fmt.Errorf("unknown SSH host key for %s", hostname)
			}

			if err := appendKnownHost(knownHostsPath, hostname, remote, key); err != nil {
				return err
			}

			logging.L().Info("ssh_host_key_accepted",
				"host", hostname,
				"fingerprint", ssh.FingerprintSHA256(key),
			)
			return nil
		}

		logging.L().Warn("ssh_host_key_changed",
			"host", hostname,
			"fingerprint", ssh.FingerprintSHA256(key),
		)
		return fmt.Errorf("SSH host key changed for %s", hostname)
	}, nil
}

func ensureKnownHostsFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create known_hosts directory: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		return nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to create known_hosts file: %w", err)
	}
	return file.Close()
}

func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	hosts := buildKnownHostsEntries(hostname, remote)
	line := knownhosts.Line(hosts, key)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open known_hosts file: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(line); err != nil {
		return fmt.Errorf("failed to write known_hosts entry: %w", err)
	}
	return nil
}

func buildKnownHostsEntries(hostname string, remote net.Addr) []string {
	var entries []string
	remoteHost, remotePort := splitHostPort(remote)

	if hostname != "" {
		entries = append(entries, formatKnownHostsHost(hostname, remotePort))
	}

	if remoteHost != "" && remoteHost != hostname {
		entries = append(entries, formatKnownHostsHost(remoteHost, remotePort))
	}

	if len(entries) == 0 {
		entries = append(entries, formatKnownHostsHost(remoteHost, remotePort))
	}

	return entries
}

func splitHostPort(remote net.Addr) (string, string) {
	if remote == nil {
		return "", ""
	}

	host, port, err := net.SplitHostPort(remote.String())
	if err != nil {
		return remote.String(), ""
	}

	return host, port
}

func formatKnownHostsHost(host string, port string) string {
	if host == "" {
		return host
	}

	if port == "" || port == "22" {
		return host
	}

	return fmt.Sprintf("[%s]:%s", host, port)
}
