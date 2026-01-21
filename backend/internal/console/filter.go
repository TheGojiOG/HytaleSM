package console

import (
	"database/sql"
	"regexp"
	"strings"
	"time"
)

// OutputFilter filters console output based on criteria
type OutputFilter struct {
	FilterType string // "none", "errors", "search", "regex"
	Pattern    string
	CaseSensitive bool
	regex      *regexp.Regexp
}

// FilterResult represents the result of filtering a line
type FilterResult struct {
	Include   bool
	Highlight []int // Start/end positions of matches for highlighting
}

// NewOutputFilter creates a new output filter
func NewOutputFilter(filterType, pattern string, caseSensitive bool) (*OutputFilter, error) {
	filter := &OutputFilter{
		FilterType:    filterType,
		Pattern:       pattern,
		CaseSensitive: caseSensitive,
	}

	// Compile regex if needed
	if filterType == "regex" && pattern != "" {
		flags := ""
		if !caseSensitive {
			flags = "(?i)"
		}
		compiled, err := regexp.Compile(flags + pattern)
		if err != nil {
			return nil, err
		}
		filter.regex = compiled
	}

	return filter, nil
}

// Filter applies the filter to a line of output
func (f *OutputFilter) Filter(line string) FilterResult {
	result := FilterResult{
		Include:   true,
		Highlight: []int{},
	}

	switch f.FilterType {
	case "none":
		// Include everything
		return result

	case "errors":
		// Filter for error/warning keywords
		result.Include = f.isErrorLine(line)
		if result.Include {
			// Highlight error keywords
			result.Highlight = f.highlightErrors(line)
		}
		return result

	case "search":
		// Simple text search
		if f.Pattern == "" {
			return result
		}

		searchLine := line
		searchPattern := f.Pattern
		if !f.CaseSensitive {
			searchLine = strings.ToLower(line)
			searchPattern = strings.ToLower(f.Pattern)
		}

		if idx := strings.Index(searchLine, searchPattern); idx >= 0 {
			result.Include = true
			result.Highlight = []int{idx, idx + len(f.Pattern)}
		} else {
			result.Include = false
		}
		return result

	case "regex":
		// Regex matching
		if f.regex == nil {
			return result
		}

		if match := f.regex.FindStringIndex(line); match != nil {
			result.Include = true
			result.Highlight = match
		} else {
			result.Include = false
		}
		return result

	default:
		return result
	}
}

// isErrorLine checks if a line contains error/warning keywords
func (f *OutputFilter) isErrorLine(line string) bool {
	lowerLine := strings.ToLower(line)

	errorKeywords := []string{
		"error",
		"exception",
		"fatal",
		"warning",
		"warn",
		"failed",
		"failure",
		"critical",
		"panic",
		"stack trace",
		"traceback",
	}

	for _, keyword := range errorKeywords {
		if strings.Contains(lowerLine, keyword) {
			return true
		}
	}

	return false
}

// highlightErrors finds positions of error keywords for highlighting
func (f *OutputFilter) highlightErrors(line string) []int {
	lowerLine := strings.ToLower(line)

	errorKeywords := []string{
		"error",
		"exception",
		"fatal",
		"warning",
		"warn",
		"failed",
	}

	for _, keyword := range errorKeywords {
		if idx := strings.Index(lowerLine, keyword); idx >= 0 {
			return []int{idx, idx + len(keyword)}
		}
	}

	return []int{}
}

// FilterLines applies the filter to multiple lines
func (f *OutputFilter) FilterLines(lines []string) []string {
	if f.FilterType == "none" {
		return lines
	}

	filtered := []string{}
	for _, line := range lines {
		result := f.Filter(line)
		if result.Include {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

// CommandHistory provides command history management
type CommandHistory struct {
	db *sql.DB
}

// NewCommandHistory creates a new command history manager
func NewCommandHistory(db *sql.DB) *CommandHistory {
	return &CommandHistory{db: db}
}

// GetRecentCommands returns recent commands for a server
func (ch *CommandHistory) GetRecentCommands(serverID string, limit int) ([]CommandRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := ch.db.Query(`
		SELECT c.id, c.server_id, c.user_id, u.username, c.command, c.executed_at, c.success, c.output_preview
		FROM console_commands c
		JOIN users u ON c.user_id = u.id
		WHERE c.server_id = ?
		ORDER BY c.executed_at DESC
		LIMIT ?
	`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	commands := []CommandRecord{}
	for rows.Next() {
		var cmd CommandRecord
		var outputPreview sql.NullString
		err := rows.Scan(&cmd.ID, &cmd.ServerID, &cmd.UserID, &cmd.Username, &cmd.Command, &cmd.ExecutedAt, &cmd.Success, &outputPreview)
		if err != nil {
			return nil, err
		}
		if outputPreview.Valid {
			cmd.OutputPreview = outputPreview.String
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

// GetUserCommands returns commands executed by a specific user
func (ch *CommandHistory) GetUserCommands(userID int64, limit int) ([]CommandRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := ch.db.Query(`
		SELECT c.id, c.server_id, c.user_id, u.username, c.command, c.executed_at, c.success, c.output_preview
		FROM console_commands c
		JOIN users u ON c.user_id = u.id
		WHERE c.user_id = ?
		ORDER BY c.executed_at DESC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	commands := []CommandRecord{}
	for rows.Next() {
		var cmd CommandRecord
		var outputPreview sql.NullString
		err := rows.Scan(&cmd.ID, &cmd.ServerID, &cmd.UserID, &cmd.Username, &cmd.Command, &cmd.ExecutedAt, &cmd.Success, &outputPreview)
		if err != nil {
			return nil, err
		}
		if outputPreview.Valid {
			cmd.OutputPreview = outputPreview.String
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

// SearchCommands searches command history
func (ch *CommandHistory) SearchCommands(serverID, query string, limit int) ([]CommandRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := ch.db.Query(`
		SELECT c.id, c.server_id, c.user_id, u.username, c.command, c.executed_at, c.success, c.output_preview
		FROM console_commands c
		JOIN users u ON c.user_id = u.id
		WHERE c.server_id = ? AND c.command LIKE ?
		ORDER BY c.executed_at DESC
		LIMIT ?
	`, serverID, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	commands := []CommandRecord{}
	for rows.Next() {
		var cmd CommandRecord
		var outputPreview sql.NullString
		err := rows.Scan(&cmd.ID, &cmd.ServerID, &cmd.UserID, &cmd.Username, &cmd.Command, &cmd.ExecutedAt, &cmd.Success, &outputPreview)
		if err != nil {
			return nil, err
		}
		if outputPreview.Valid {
			cmd.OutputPreview = outputPreview.String
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

// GetAutocomplete returns autocomplete suggestions from command history
func (ch *CommandHistory) GetAutocomplete(serverID, prefix string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := ch.db.Query(`
		SELECT DISTINCT command
		FROM console_commands
		WHERE server_id = ? AND command LIKE ?
		ORDER BY executed_at DESC
		LIMIT ?
	`, serverID, prefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	suggestions := []string{}
	for rows.Next() {
		var cmd string
		if err := rows.Scan(&cmd); err != nil {
			return nil, err
		}
		suggestions = append(suggestions, cmd)
	}

	return suggestions, nil
}

// CommandRecord represents a command history record
type CommandRecord struct {
	ID            int64     `json:"id"`
	ServerID      string    `json:"server_id"`
	UserID        int64     `json:"user_id"`
	Username      string    `json:"username"`
	Command       string    `json:"command"`
	ExecutedAt    time.Time `json:"executed_at"`
	Success       bool      `json:"success"`
	OutputPreview string    `json:"output_preview,omitempty"`
}
