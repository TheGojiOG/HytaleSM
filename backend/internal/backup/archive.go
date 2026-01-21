package backup

import (
	"fmt"
	"log"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheGojiOG/HytaleSM/internal/ssh"
)

// ArchiveHandler creates and manages tar.gz archives via SSH
type ArchiveHandler struct {
	sshPool *ssh.ConnectionPool
}

// ArchiveInfo contains metadata about a created archive
type ArchiveInfo struct {
	Filename    string
	Path        string
	SizeBytes   int64
	CreatedAt   time.Time
	Directories []string
	FileCount   int
	Compression CompressionConfig
}

// ArchiveOptions contains optional settings for archive creation
type ArchiveOptions struct {
	Compression CompressionConfig
	RunAsUser   string
	UseSudo     bool
}

// NewArchiveHandler creates a new archive handler
func NewArchiveHandler(pool *ssh.ConnectionPool) *ArchiveHandler {
	return &ArchiveHandler{
		sshPool: pool,
	}
}

// CreateArchive creates a tar.gz archive of specified directories on the remote server
func (ah *ArchiveHandler) CreateArchive(serverID string, directories []string, exclude []string, workingDir string, options ArchiveOptions) (*ArchiveInfo, error) {
	conn := ah.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return nil, fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	compression := normalizeCompression(options.Compression)
	archiveExt := compressionArchiveExtension(compression)

	// Generate unique filename with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("backup_%s.%s", timestamp, archiveExt)
	archivePath := path.Join(workingDir, filename)

	log.Printf("[Archive] Creating archive %s for server %s", filename, serverID)
	log.Printf("[Archive] Backing up directories: %v from %s", directories, workingDir)

	// Validate directories exist (relative to workingDir)
	for _, dir := range directories {
		fullPath := path.Join(workingDir, dir)
		checkCmd := fmt.Sprintf("test -d '%s' || test -f '%s'", fullPath, fullPath)
		_, err := ah.runCommand(conn, checkCmd, options)
		if err != nil {
			return nil, fmt.Errorf("directory or file does not exist: %s (checked: %s)", dir, fullPath)
		}
	}

	// Create temporary directory for staging if needed
	tempDir := path.Join(workingDir, fmt.Sprintf(".backup_temp_%d", time.Now().Unix()))
	createTempCmd := fmt.Sprintf("mkdir -p '%s'", tempDir)
	if _, err := ah.runCommand(conn, createTempCmd, options); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Build tar command
	// Use relative paths within the working directory
	tarCmd := ah.buildTarCommand(directories, exclude, archivePath, workingDir, compression)
	
	log.Printf("[Archive] Running tar command: %s", tarCmd)
	output, err := ah.runCommand(conn, tarCmd, options)
	if err != nil {
		// Cleanup temp dir on failure
		ah.runCommand(conn, fmt.Sprintf("rm -rf '%s'", tempDir), options)
		return nil, fmt.Errorf("failed to create archive: %w (output: %s)", err, output)
	}

	// Cleanup temp directory
	if _, err := ah.runCommand(conn, fmt.Sprintf("rm -rf '%s'", tempDir), options); err != nil {
		log.Printf("[Archive] Warning: Failed to cleanup temp directory: %v", err)
	}

	// Get archive size
	sizeCmd := fmt.Sprintf("stat -c%%s '%s'", archivePath)
	sizeOutput, err := ah.runCommand(conn, sizeCmd, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get archive size: %w", err)
	}

	var sizeBytes int64
	if _, err := fmt.Sscanf(strings.TrimSpace(sizeOutput), "%d", &sizeBytes); err != nil {
		return nil, fmt.Errorf("failed to parse archive size: %w", err)
	}

	// Count files in archive
	fileCountCmd := fmt.Sprintf("tar -%sf '%s' | wc -l", tarListFlag(compression), archivePath)
	countOutput, err := ah.runCommand(conn, fileCountCmd, options)
	if err != nil {
		log.Printf("[Archive] Warning: Failed to count files: %v", err)
	}

	var fileCount int
	fmt.Sscanf(strings.TrimSpace(countOutput), "%d", &fileCount)

	info := &ArchiveInfo{
		Filename:    filename,
		Path:        archivePath,
		SizeBytes:   sizeBytes,
		CreatedAt:   time.Now(),
		Directories: directories,
		FileCount:   fileCount,
		Compression: compression,
	}

	log.Printf("[Archive] Archive created successfully: %s (size: %d bytes, files: %d)", 
		filename, sizeBytes, fileCount)

	return info, nil
}

// buildTarCommand constructs the tar command for creating the archive
func (ah *ArchiveHandler) buildTarCommand(directories []string, exclude []string, archivePath, workingDir string, compression CompressionConfig) string {
	// Make paths relative to working directory for cleaner archives
	var relativePaths []string
	for _, dir := range directories {
		// If path is absolute, try to make it relative to workingDir
		if path.IsAbs(dir) {
			relPath, err := filepath.Rel(workingDir, dir)
			if err == nil && !strings.HasPrefix(relPath, "..") {
				relativePaths = append(relativePaths, relPath)
			} else {
				relativePaths = append(relativePaths, dir)
			}
		} else {
			relativePaths = append(relativePaths, dir)
		}
	}

	// Build tar command with compression
	// -c: create, -z: gzip, -f: file
	// -C: change to directory before archiving
	targets := strings.Join(relativePaths, "' '")
	excludeArgs := buildExcludeArgs(exclude)
	compressionFlag := tarCreateFlag(compression)
	compressionEnv := tarCompressionEnv(compression)
	return fmt.Sprintf("cd '%s' && %s tar -%s '%s' %s '%s' 2>&1", 
		workingDir, compressionEnv, compressionFlag, archivePath, excludeArgs, targets)
}

// ExtractArchive extracts a tar.gz archive to a specified destination
func (ah *ArchiveHandler) ExtractArchive(serverID, archivePath, destination string) error {
	conn := ah.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	log.Printf("[Archive] Extracting archive %s to %s", archivePath, destination)

	// Ensure destination directory exists
	mkdirCmd := fmt.Sprintf("mkdir -p '%s'", destination)
	if _, err := ah.runCommand(conn, mkdirCmd, ArchiveOptions{}); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Extract archive
	// -xzf: extract, gzip decompress, file
	// -C: change to directory before extracting
	compression := detectCompressionFromFilename(archivePath)
	extractCmd := fmt.Sprintf("tar -%s '%s' -C '%s' 2>&1", tarExtractFlag(compression), archivePath, destination)
	output, err := ah.runCommand(conn, extractCmd, ArchiveOptions{})
	if err != nil {
		return fmt.Errorf("failed to extract archive: %w (output: %s)", err, output)
	}

	log.Printf("[Archive] Archive extracted successfully to %s", destination)
	return nil
}

// ListArchiveContents lists the contents of a tar.gz archive
func (ah *ArchiveHandler) ListArchiveContents(serverID, archivePath string) ([]string, error) {
	conn := ah.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return nil, fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// List archive contents
	compression := detectCompressionFromFilename(archivePath)
	listCmd := fmt.Sprintf("tar -%s '%s'", tarListFlag(compression), archivePath)
	output, err := ah.runCommand(conn, listCmd, ArchiveOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list archive contents: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	return lines, nil
}

// DeleteArchive removes an archive from the remote server
func (ah *ArchiveHandler) DeleteArchive(serverID, archivePath string) error {
	conn := ah.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	log.Printf("[Archive] Deleting archive %s", archivePath)

	deleteCmd := fmt.Sprintf("rm -f '%s'", archivePath)
	if _, err := ah.runCommand(conn, deleteCmd, ArchiveOptions{}); err != nil {
		return fmt.Errorf("failed to delete archive: %w", err)
	}

	log.Printf("[Archive] Archive deleted successfully")
	return nil
}

// DeleteArchiveWithOptions removes an archive from the remote server with sudo/run-as-user options
func (ah *ArchiveHandler) DeleteArchiveWithOptions(serverID, archivePath string, options ArchiveOptions) error {
	conn := ah.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	log.Printf("[Archive] Deleting archive %s", archivePath)

	deleteCmd := fmt.Sprintf("rm -f '%s'", archivePath)
	if _, err := ah.runCommand(conn, deleteCmd, options); err != nil {
		return fmt.Errorf("failed to delete archive: %w", err)
	}

	log.Printf("[Archive] Archive deleted successfully")
	return nil
}

// GetArchiveInfo retrieves information about an existing archive
func (ah *ArchiveHandler) GetArchiveInfo(serverID, archivePath string) (*ArchiveInfo, error) {
	conn := ah.sshPool.GetExistingConnection(serverID)
	if conn == nil {
		return nil, fmt.Errorf("no SSH connection available for server %s", serverID)
	}

	// Check if archive exists
	checkCmd := fmt.Sprintf("test -f '%s'", archivePath)
	if _, err := ah.runCommand(conn, checkCmd, ArchiveOptions{}); err != nil {
		return nil, fmt.Errorf("archive does not exist: %s", archivePath)
	}

	// Get archive size
	sizeCmd := fmt.Sprintf("stat -c%%s '%s'", archivePath)
	sizeOutput, err := ah.runCommand(conn, sizeCmd, ArchiveOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get archive size: %w", err)
	}

	var sizeBytes int64
	if _, err := fmt.Sscanf(strings.TrimSpace(sizeOutput), "%d", &sizeBytes); err != nil {
		return nil, fmt.Errorf("failed to parse archive size: %w", err)
	}

	// Get modification time
	timeCmd := fmt.Sprintf("stat -c%%Y '%s'", archivePath)
	timeOutput, err := ah.runCommand(conn, timeCmd, ArchiveOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get archive time: %w", err)
	}

	var unixTime int64
	fmt.Sscanf(strings.TrimSpace(timeOutput), "%d", &unixTime)
	createdAt := time.Unix(unixTime, 0)

	// Count files
	fileCountCmd := fmt.Sprintf("tar -tzf '%s' | wc -l", archivePath)
	countOutput, err := ah.runCommand(conn, fileCountCmd, ArchiveOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to count files: %w", err)
	}

	var fileCount int
	fmt.Sscanf(strings.TrimSpace(countOutput), "%d", &fileCount)

	info := &ArchiveInfo{
		Filename:  path.Base(archivePath),
		Path:      archivePath,
		SizeBytes: sizeBytes,
		CreatedAt: createdAt,
		FileCount: fileCount,
	}

	return info, nil
}

func (ah *ArchiveHandler) runCommand(conn *ssh.PooledConnection, command string, options ArchiveOptions) (string, error) {
	wrapped := wrapCommandForUser(command, options)
	return conn.Client.RunCommand(wrapped)
}

func wrapCommandForUser(command string, options ArchiveOptions) string {
	if !options.UseSudo {
		return command
	}

	escaped := escapeSingleQuotes(command)
	if strings.TrimSpace(options.RunAsUser) != "" {
		return fmt.Sprintf("sudo -u %s -- sh -c '%s'", escapeSingleQuotes(options.RunAsUser), escaped)
	}

	return fmt.Sprintf("sudo -- sh -c '%s'", escaped)
}
