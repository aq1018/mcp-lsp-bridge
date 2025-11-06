package lsp

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"rockerboo/mcp-lsp-bridge/types"

	"github.com/myleshyson/lsprotocol-go/protocol"
)

// Language represents a programming language
type Language string

// GlobalConfig holds global configuration options
type GlobalConfig struct {
	LogPath            string `json:"log_file_path"`
	LogLevel           string `json:"log_level"`
	LogOutput          string `json:"log_output"` // "file", "stderr", "both"
	MaxLogFiles        int    `json:"max_log_files"`
	MaxRestartAttempts int    `json:"max_restart_attempts"`
	RestartDelayMs     int    `json:"restart_delay_ms"`
}

// LanguageServerConfig represents configuration for a single language server
type LanguageServerConfig struct {
	Command               string                 `json:"command"`
	Args                  []string               `json:"args"`
	Languages             []string               `json:"languages,omitempty"`
	Filetypes             []string               `json:"filetypes"`
	InitializationOptions map[string]interface{} `json:"initialization_options,omitempty"`
}

// GetCommand implements types.LanguageServerConfigProvider
func (c *LanguageServerConfig) GetCommand() string {
	return c.Command
}

// GetArgs implements types.LanguageServerConfigProvider
func (c *LanguageServerConfig) GetArgs() []string {
	return c.Args
}

// GetInitializationOptions implements types.LanguageServerConfigProvider
func (c *LanguageServerConfig) GetInitializationOptions() map[string]interface{} {
	return c.InitializationOptions
}

// LSPServerConfig represents the complete LSP server configuration
type LSPServerConfig struct {
	Imports                []string                                      `json:"imports,omitempty"`
	Global                 GlobalConfig                                  `json:"global"`
	LanguageServers        map[types.LanguageServer]LanguageServerConfig `json:"language_servers"`
	WorkspaceConfiguration map[string]interface{}                        `json:"workspace_configuration,omitempty"`
	LanguageServerMap      map[types.LanguageServer][]types.Language     `json:"language_server_map,omitempty"`
	ExtensionLanguageMap   map[string]types.Language                     `json:"extension_language_map,omitempty"`
	PreferredFormatters    map[string]string                             `json:"preferred_formatters,omitempty"`
}

// LanguageClient wraps a Language Server Protocol client connection
type LanguageClient struct {
	mu                 sync.RWMutex
	conn               types.LSPConnectionInterface
	ctx                context.Context
	cancel             context.CancelFunc
	cmd                *exec.Cmd
	clientCapabilities protocol.ClientCapabilities
	serverCapabilities protocol.ServerCapabilities

	tokenParser types.SemanticTokensParserProvider

	workspacePaths         []string
	initializationOptions  map[string]interface{}
	workspaceConfiguration map[string]interface{} // Workspace configuration sections
	workingDir             string                  // Working directory for the language server process

	// Connection management
	command         string
	args            []string
	processID       int32
	lastInitialized time.Time
	status          ClientStatus
	lastError       error

	// Metrics
	totalRequests      int64
	successfulRequests int64
	failedRequests     int64
	lastErrorTime      time.Time

	// Configuration
	maxConnectionAttempts int
	connectionTimeout     time.Duration
	idleTimeout           time.Duration
	restartDelay          time.Duration

	// Diagnostic cache for push-based diagnostics
	diagnosticCache map[protocol.DocumentUri][]protocol.Diagnostic
	diagnosticMutex sync.RWMutex

	// Handler for LSP notifications
	Handler *ClientHandler

	// Track open documents to prevent duplicate didOpen notifications
	openDocuments      map[protocol.DocumentUri]bool
	openDocumentsMutex sync.RWMutex
}
