package backup

import (
	"path"
	"strconv"
	"strings"
)

// CompressionConfig controls archive compression
// Type values: "gzip", "none"
type CompressionConfig struct {
	Type  string `json:"type"`
	Level int    `json:"level,omitempty"`
}

func normalizeCompression(config CompressionConfig) CompressionConfig {
	compressionType := strings.ToLower(strings.TrimSpace(config.Type))
	if compressionType == "" {
		compressionType = "gzip"
	}

	level := config.Level
	if level == 0 {
		level = 6
	}
	if level < 1 {
		level = 1
	}
	if level > 9 {
		level = 9
	}

	if compressionType != "gzip" && compressionType != "none" {
		compressionType = "gzip"
	}

	return CompressionConfig{
		Type:  compressionType,
		Level: level,
	}
}

func compressionArchiveExtension(config CompressionConfig) string {
	switch normalizeCompression(config).Type {
	case "none":
		return "tar"
	default:
		return "tar.gz"
	}
}

func detectCompressionFromFilename(filename string) CompressionConfig {
	base := strings.ToLower(path.Base(filename))
	switch {
	case strings.HasSuffix(base, ".tar.gz") || strings.HasSuffix(base, ".tgz"):
		return CompressionConfig{Type: "gzip", Level: 6}
	case strings.HasSuffix(base, ".tar"):
		return CompressionConfig{Type: "none"}
	default:
		return CompressionConfig{Type: "gzip", Level: 6}
	}
}

func tarCreateFlag(config CompressionConfig) string {
	switch normalizeCompression(config).Type {
	case "none":
		return "cf"
	default:
		return "czf"
	}
}

func tarExtractFlag(config CompressionConfig) string {
	switch normalizeCompression(config).Type {
	case "none":
		return "xf"
	default:
		return "xzf"
	}
}

func tarListFlag(config CompressionConfig) string {
	switch normalizeCompression(config).Type {
	case "none":
		return "tf"
	default:
		return "tzf"
	}
}

func tarCompressionEnv(config CompressionConfig) string {
	compression := normalizeCompression(config)
	if compression.Type != "gzip" {
		return ""
	}

	return "GZIP=-" + strconv.Itoa(compression.Level)
}

func buildExcludeArgs(exclude []string) string {
	if len(exclude) == 0 {
		return ""
	}

	var parts []string
	for _, entry := range exclude {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		parts = append(parts, "--exclude='"+escapeSingleQuotes(trimmed)+"'")
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, " ") + " "
}

func escapeSingleQuotes(value string) string {
	return strings.ReplaceAll(value, "'", "'\\''")
}
