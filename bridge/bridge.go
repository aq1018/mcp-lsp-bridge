package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/lsp"
	"rockerboo/mcp-lsp-bridge/lsp/capabilities"
	"rockerboo/mcp-lsp-bridge/security"
	"rockerboo/mcp-lsp-bridge/types"
	"rockerboo/mcp-lsp-bridge/utils"

	"github.com/mark3labs/mcp-go/server"
	"github.com/myleshyson/lsprotocol-go/protocol"
)

// NewMCPLSPBridge creates a new bridge instance with provided configuration and client factory
func NewMCPLSPBridge(config types.LSPServerConfigProvider, allowedDirectories []string) *MCPLSPBridge {
	bridge := &MCPLSPBridge{
		clients:            make(map[types.LanguageServer]types.LanguageClientInterface),
		config:             config,
		allowedDirectories: allowedDirectories,
	}

	return bridge
}

// ConnectionAttemptConfig defines retry parameters for language server connections
type ConnectionAttemptConfig struct {
	MaxRetries   int
	RetryDelay   time.Duration
	TotalTimeout time.Duration
}

// DefaultConnectionConfig provides a default configuration for connection attempts
func DefaultConnectionConfig() ConnectionAttemptConfig {
	return ConnectionAttemptConfig{
		MaxRetries:   3,
		RetryDelay:   2 * time.Second,
		TotalTimeout: 30 * time.Second,
	}
}

func (b *MCPLSPBridge) IsAllowedDirectory(path string) (string, error) {
	return security.ValidateConfigPath(path, b.allowedDirectories)
}

func (b *MCPLSPBridge) AllowedDirectories() []string {
	return b.allowedDirectories
}

// validateAndConnectClient attempts to validate and establish a language server connection using the injected factory
func (b *MCPLSPBridge) validateAndConnectClient(language string, serverConfig types.LanguageServerConfigProvider, config ConnectionAttemptConfig) (types.LanguageClientInterface, error) {
	// Attempt connection with retry mechanism
	var lastErr error

	startTime := time.Now()

	// Get current working directory for debugging
	cwd, err := os.Getwd()
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to get current working directory: %v", err))
	} else {
		logger.Debug(fmt.Sprintf("validateAndConnectClient: Bridge process CWD: %s", cwd))
	}

	// Prefer MCP workspace roots over allowed directories
	var dirs []string
	if roots := b.GetWorkspaceRoots(); len(roots) > 0 {
		dirs = roots
		logger.Debug(fmt.Sprintf("validateAndConnectClient: Using MCP workspace roots: %v", dirs))
	} else {
		dirs = b.AllowedDirectories()
		logger.Debug(fmt.Sprintf("validateAndConnectClient: Using allowed directories (no MCP roots): %v", dirs))
	}

	if len(dirs) == 0 {
		return nil, fmt.Errorf("no workspace directories available (neither MCP roots nor allowed directories set)")
	}

	dir := dirs[0] // Get first directory (for now)

	logger.Debug(fmt.Sprintf("validateAndConnectClient: Using directory: %s", dir))

	absPath, err := b.IsAllowedDirectory(dir)
	if err != nil {
		return nil, fmt.Errorf("file path is not allowed: %s", err)
	}

	for attempt := 0; attempt < config.MaxRetries; attempt++ {
		// Check if total timeout exceeded
		if time.Since(startTime) > config.TotalTimeout {
			break
		}

		// Create language client using the factory
		var client *lsp.LanguageClient

		var err error

		client, err = lsp.NewLanguageClient(serverConfig.GetCommand(), serverConfig.GetArgs()...)
		if err != nil {
			lastErr = fmt.Errorf("failed to create language client on attempt %d: %w", attempt+1, err)
			continue
		}

		// Set working directory so relative paths in config work for all team members
		client.SetWorkingDirectory(dir)

		_, err = client.Connect()
		if err != nil {
			lastErr = fmt.Errorf("failed to connect to the LSP on attempt %d: %w", attempt+1, err)

			time.Sleep(config.RetryDelay)

			continue
		}

		rootPath := "file://" + absPath
		// root_uri := protocol.DocumentUri(root_path)

		logger.Debug("validateAndConnectClient: Root path for LSP: " + rootPath)
		// Process IDs are typically small positive integers, safe to convert
		// But we'll add bounds checking for completeness
		pid := os.Getpid()
		if pid < 0 || pid > math.MaxInt32 {
			return nil, fmt.Errorf("process ID out of range: %d", pid)
		}
		process_id := int32(pid)

		// Prepare initialization parameters
		workspaceFolders := []protocol.WorkspaceFolder{
			{
				Uri:  protocol.URI(rootPath),
				Name: filepath.Base(absPath),
			},
		}

		client.SetProjectRoots([]string{dir})

		params := protocol.InitializeParams{
			ProcessId: &process_id,
			ClientInfo: &protocol.ClientInfo{
				Name:    "MCP-LSP Bridge",
				Version: "1.0.0",
			},
			// RootUri:          &root_uri,
			WorkspaceFolders: &workspaceFolders,
			// Capabilities are auto-generated from registered features
			// See lsp/capabilities/features.go for the complete list
			Capabilities: capabilities.Registry.Build(),
		}

		logger.Debug(fmt.Sprintf("INIT: Sending capabilities - workspace.configuration=%v, workspace.workspaceFolders=%v",
			params.Capabilities.Workspace.Configuration, params.Capabilities.Workspace.WorkspaceFolders))

		// Apply any initialization options from the configuration
		if serverConfig.GetInitializationOptions() != nil {
			params.InitializationOptions = serverConfig.GetInitializationOptions()
			// Store initialization settings in the client for workspace/configuration requests
			client.SetInitializationSettings(serverConfig.GetInitializationOptions())
		}

		// Store workspace configuration in the client for dynamic workspace/configuration requests
		if lspConfig, ok := b.config.(*lsp.LSPServerConfig); ok {
			if lspConfig.WorkspaceConfiguration != nil {
				client.SetWorkspaceConfiguration(lspConfig.WorkspaceConfiguration)
				logger.Debug(fmt.Sprintf("Set workspace configuration with %d sections", len(lspConfig.WorkspaceConfiguration)))
			}
		}

		// Check connection status before initialize
		metrics := client.GetMetrics()
		logger.Debug(fmt.Sprintf("STATUS: Before Initialize - Client connected: %v, ctx.Err(): %v", metrics.IsConnected(), client.Context().Err()))

		logger.Debug(fmt.Sprintf("STATUS: client %+v", client))

		// Send initialize request
		result, err := client.Initialize(params)
		if err != nil {
			lastErr = fmt.Errorf("initialize request failed on attempt %d: %w", attempt+1, err)
			logger.Error(lastErr)

			err = client.Close()
			if err != nil {
				return nil, err
			}

			time.Sleep(config.RetryDelay)

			continue
		}

		logger.Debug(fmt.Sprintf("STATUS: After Initialize - Client connected: %v, ctx.Err(): %v", metrics.IsConnected(), client.Context().Err()))
		logger.Debug(fmt.Sprintf("STATUS: Initialize result: %+v", result))
		logger.Debug(fmt.Sprintf("STATUS: Setting up semantic tokens provider: %+v", client.ServerCapabilities().SemanticTokensProvider))

		// Set server capabilities
		client.SetServerCapabilities(result.Capabilities)

		semanticTokensProvider := client.ServerCapabilities().SemanticTokensProvider

		var supportsSemanticTokens bool

		if semanticTokensProvider == nil {
			logger.Warn("No semantic tokens provider found")

			supportsSemanticTokens = false
		} else {
			switch semanticTokensProvider.Value.(type) {
			case bool:
				logger.Debug("Semantic tokens supported")

				supportsSemanticTokens = true
			default:
				logger.Warn("Unknown semantic tokens provider")

				supportsSemanticTokens = false
			}

			if supportsSemanticTokens {
				err = client.SetupSemanticTokens()
				if err != nil {
					// Semantic tokens setup failure is non-fatal - many servers don't support this feature
					logger.Warn(fmt.Sprintf("Failed to setup semantic tokens on attempt %d (non-fatal): %v", attempt+1, err))
					// Continue without semantic tokens support
				}
			}
		}

		// Log server info and capabilities
		if result.ServerInfo != nil {
			logger.Debug(fmt.Sprintf("Initialize result - Server Info: %+v", *result.ServerInfo))
		}

		logger.Debug(fmt.Sprintf("Initialize result - Capabilities: %+v", result.Capabilities))

		// Enhanced logging for Workspace and WorkspaceFolders capabilities
		if result.Capabilities.Workspace != nil {
			logger.Debug(fmt.Sprintf("Workspace Capabilities: %+v", *result.Capabilities.Workspace))

			// Specifically log WorkspaceFolders support
			if result.Capabilities.Workspace.WorkspaceFolders != nil {
				logger.Debug(fmt.Sprintf("WorkspaceFolders Support: Supported = %v",
					*result.Capabilities.Workspace.WorkspaceFolders))
			} else {
				logger.Warn("WorkspaceFolders Capability is nil")
			}
		} else {
			logger.Warn("Workspace Capabilities are nil")
		}

		// Send initialized notification
		err = client.Initialized()
		if err != nil {
			lastErr = fmt.Errorf("failed to send initialized notification on attempt %d: %w", attempt+1, err)

			err = client.Close()
			if err != nil {
				return nil, err
			}

			time.Sleep(config.RetryDelay)

			continue
		}

		// Successfully connected
		return client, nil
	}

	return nil, fmt.Errorf("failed to establish language server connection for %s after %d attempts: %w",
		language, config.MaxRetries, lastErr)
}

// GetClientForLanguage retrieves or creates a language server client for a specific language
func (b *MCPLSPBridge) GetClientForLanguage(language string) (types.LanguageClientInterface, error) {
	// Look up the server name for the given language
	server := b.config.GetServerNameFromLanguage(types.Language(language))
	if server == "" {
		return nil, fmt.Errorf("no server found for language %s", language)
	}

	// Check if client already exists
	if existingClient, exists := b.clients[server]; exists {
		// Check if client context is still valid (not cancelled)
		if existingClient.Context().Err() == nil {
			// Reset status to connected if it was in error state but context is still valid
			metrics := existingClient.GetMetrics()
			logger.Debug(fmt.Sprintf("GetClientForLanguage: Existing client for %s, metrics: %+v", language, metrics))

			return existingClient, nil
		}
		// Client context is cancelled, remove it and create a new one
		logger.Warn("Removing client with cancelled context for language " + language)

		err := existingClient.Close()
		if err != nil {
			return nil, err
		}

		delete(b.clients, server)
	}

	// Find the server configuration
	serverConfig, err := b.GetConfig().FindServerConfig(language)
	if err != nil {
		return nil, fmt.Errorf("no server configuration found for language %s", language)
	}

	// Attempt to connect with default configuration
	client, err := b.validateAndConnectClient(language, serverConfig, DefaultConnectionConfig())
	if err != nil {
		return nil, err
	}

	// Store the new client
	b.clients[server] = client

	// Set diagnostics callback if one is registered
	if b.diagnosticsCallback != nil {
		if lspClient, ok := client.(*lsp.LanguageClient); ok {
			if lspClient.Handler != nil {
				lspClient.Handler.SetDiagnosticsCallback(b.diagnosticsCallback)
			}
		}
	}

	return client, nil
}

// GetAllClientsForLanguage retrieves or creates all language server clients for a specific language
func (b *MCPLSPBridge) GetAllClientsForLanguage(language string) ([]types.LanguageClientInterface, []types.LanguageServer, error) {
	// Find all server configurations for this language
	serverConfigs, serverNames, err := b.config.FindAllServerConfigs(language)
	if err != nil {
		return nil, nil, fmt.Errorf("no server configurations found for language %s: %w", language, err)
	}

	var clients []types.LanguageClientInterface
	var validServerNames []types.LanguageServer

	for i, serverName := range serverNames {
		serverConfig := serverConfigs[i]

		// Check if client already exists
		if existingClient, exists := b.clients[serverName]; exists {
			// Check if client context is still valid (not cancelled)
			if existingClient.Context().Err() == nil {
				clients = append(clients, existingClient)
				validServerNames = append(validServerNames, serverName)
				continue
			}
			// Client context is cancelled, remove it and create a new one
			logger.Warn("Removing client with cancelled context for language " + language + " server " + string(serverName))

			err := existingClient.Close()
			if err != nil {
				logger.Error(fmt.Sprintf("Error closing cancelled client for %s: %v", serverName, err))
			}

			delete(b.clients, serverName)
		}

		// Attempt to connect with default configuration
		client, err := b.validateAndConnectClient(language, serverConfig, DefaultConnectionConfig())
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to connect to server %s for language %s: %v", serverName, language, err))
			continue // Skip this server, try others
		}

		// Store the new client
		b.clients[serverName] = client

		// Set diagnostics callback if one is registered
		if b.diagnosticsCallback != nil {
			if lspClient, ok := client.(*lsp.LanguageClient); ok {
				if lspClient.Handler != nil {
					lspClient.Handler.SetDiagnosticsCallback(b.diagnosticsCallback)
				}
			}
		}

		clients = append(clients, client)
		validServerNames = append(validServerNames, serverName)
	}

	if len(clients) == 0 {
		return nil, nil, fmt.Errorf("failed to connect to any language servers for language %s", language)
	}

	return clients, validServerNames, nil
}

// inferLanguageIdFromURI infers the language ID from a file URI
// This is a helper for document opening operations
func inferLanguageIdFromURI(uri string) protocol.LanguageKind {
	// Extract file extension
	filePath := strings.TrimPrefix(uri, "file://")
	lastDot := strings.LastIndex(filePath, ".")
	if lastDot == -1 {
		return "plaintext" // Default to plaintext
	}

	ext := filePath[lastDot+1:]

	// Map extensions to language IDs
	switch ext {
	case "ts":
		return protocol.LanguageKindTypeScript
	case "tsx":
		return protocol.LanguageKindTypeScriptReact
	case "js":
		return protocol.LanguageKindJavaScript
	case "jsx":
		return protocol.LanguageKindJavaScriptReact
	case "go":
		return protocol.LanguageKindGo
	case "py":
		return protocol.LanguageKindPython
	case "rs":
		return protocol.LanguageKindRust
	case "java":
		return protocol.LanguageKindJava
	case "c":
		return protocol.LanguageKindC
	case "cpp", "cc", "cxx":
		return protocol.LanguageKindCPP
	case "cs":
		return protocol.LanguageKindCSharp
	case "php":
		return protocol.LanguageKindPHP
	case "rb":
		return protocol.LanguageKindRuby
	case "swift":
		return protocol.LanguageKindSwift
	case "kt":
		return "kotlin" // Not defined as constant, use string literal
	case "md":
		return protocol.LanguageKindMarkdown
	case "html":
		return protocol.LanguageKindHTML
	case "css":
		return protocol.LanguageKindCSS
	case "json":
		return protocol.LanguageKindJSON
	case "xml":
		return protocol.LanguageKindXML
	case "yaml", "yml":
		return protocol.LanguageKindYAML
	default:
		return "plaintext" // Default to plaintext
	}
}

// ensureDocumentOpenInClient ensures a document is open in a specific client
// Returns error if document cannot be opened
func (b *MCPLSPBridge) ensureDocumentOpenInClient(client types.LanguageClientInterface, uri string, languageId protocol.LanguageKind) error {
	// Check if document is already open (type assert to get IsDocumentOpen method)
	if lspClient, ok := client.(*lsp.LanguageClient); ok {
		if lspClient.IsDocumentOpen(uri) {
			logger.Debug(fmt.Sprintf("Document already open in client, skipping didOpen: %s", uri))
			return nil
		}
	}

	// Read the file content
	filePath := strings.TrimPrefix(uri, "file://")

	projectRoots := client.ProjectRoots()

	// Clean the path to resolve .. and . elements
	cleanPath := filepath.Clean(filePath)

	// Convert to absolute path
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}

	for _, allowedBaseDir := range projectRoots {
		// Validate against allowed base directory
		if !security.IsWithinAllowedDirectory(absPath, allowedBaseDir) {
			return errors.New("access denied: path outside allowed directory")
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	// Use DidOpen method
	err = client.DidOpen(uri, languageId, string(content), 1)
	if err != nil {
		return fmt.Errorf("failed to send didOpen notification: %w", err)
	}

	logger.Debug(fmt.Sprintf("Document opened in LSP server: %s (language: %s)", uri, languageId))
	return nil
}

// queryAllCapableServers queries all language servers that support a specific capability
// and returns aggregated results from all capable servers
func (b *MCPLSPBridge) queryAllCapableServers(
	language string,
	uri string,
	capabilityCheck func(*protocol.ServerCapabilities) bool,
	queryFunc func(types.LanguageClientInterface) (interface{}, error),
) ([]serverResult, error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)

	// Get ALL clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(language)
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", language, err)
	}

	logger.Info(fmt.Sprintf("queryAllCapableServers: Querying %d language server(s) for %s: %v",
		len(clients), language, serverNames))

	// Filter clients by capability
	type capableServer struct {
		client     types.LanguageClientInterface
		serverName types.LanguageServer
	}
	var capableServers []capableServer

	for i, client := range clients {
		serverName := serverNames[i]
		serverCaps := client.ServerCapabilities()

		if capabilityCheck(&serverCaps) {
			capableServers = append(capableServers, capableServer{
				client:     client,
				serverName: serverName,
			})
			logger.Debug(fmt.Sprintf("Server %s supports the requested capability", serverName))
		} else {
			logger.Debug(fmt.Sprintf("Server %s does not support the requested capability, skipping", serverName))
		}
	}

	if len(capableServers) == 0 {
		logger.Warn(fmt.Sprintf("No servers support the requested capability for language %s", language))
		return []serverResult{}, nil
	}

	logger.Info(fmt.Sprintf("Found %d capable server(s), querying concurrently", len(capableServers)))

	// Infer language ID for document opening
	languageId := inferLanguageIdFromURI(normalizedURI)

	// Query all capable servers concurrently
	results := make([]serverResult, len(capableServers))
	var wg sync.WaitGroup

	for i, cs := range capableServers {
		wg.Add(1)
		go func(index int, server capableServer) {
			defer wg.Done()

			// Create a timeout context for this query
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			logger.Debug(fmt.Sprintf("Starting query for server %s (goroutine %d/%d)",
				server.serverName, index+1, len(capableServers)))

			// Ensure document is open in this client
			err := b.ensureDocumentOpenInClient(server.client, normalizedURI, languageId)
			if err != nil {
				logger.Warn(fmt.Sprintf("Failed to open document in %s: %v - continuing anyway",
					server.serverName, err))
			}

			// Execute the query with timeout
			resultChan := make(chan interface{}, 1)
			errorChan := make(chan error, 1)

			go func() {
				result, err := queryFunc(server.client)
				if err != nil {
					errorChan <- err
				} else {
					resultChan <- result
				}
			}()

			// Wait for result or timeout
			select {
			case result := <-resultChan:
				results[index] = serverResult{
					ServerName: server.serverName,
					Result:     result,
					Error:      nil,
				}
				logger.Info(fmt.Sprintf("Query succeeded for %s", server.serverName))
			case err := <-errorChan:
				results[index] = serverResult{
					ServerName: server.serverName,
					Result:     nil,
					Error:      err,
				}
				logger.Warn(fmt.Sprintf("Query failed for %s: %v", server.serverName, err))
			case <-ctx.Done():
				results[index] = serverResult{
					ServerName: server.serverName,
					Result:     nil,
					Error:      fmt.Errorf("query timeout after 5 seconds"),
				}
				logger.Warn(fmt.Sprintf("Query timeout for %s", server.serverName))
			}
		}(i, cs)
	}

	wg.Wait()
	logger.Info(fmt.Sprintf("All queries completed, returning %d results", len(results)))

	return results, nil
}

// CloseAllClients closes all active language server clients
func (b *MCPLSPBridge) CloseAllClients() {
	for serverName, client := range b.clients {
		err := client.Close()
		if err != nil {
			logger.Error(fmt.Errorf("failed to close client for %s: %w", serverName, err))
		}
	}

	b.clients = make(map[types.LanguageServer]types.LanguageClientInterface)
}

// SetDiagnosticsCallback sets a callback to be called when diagnostics are received
// This callback will be set on all existing and future LSP clients
func (b *MCPLSPBridge) SetDiagnosticsCallback(callback func(uri string)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.diagnosticsCallback = callback

	// Set callback on all existing clients
	for _, client := range b.clients {
		if lspClient, ok := client.(*lsp.LanguageClient); ok {
			if lspClient.Handler != nil {
				lspClient.Handler.SetDiagnosticsCallback(callback)
			}
		}
	}

	logger.Debug("Diagnostics callback registered for all LSP clients")
}

// InferLanguage infers the programming language from a file path
func (b *MCPLSPBridge) InferLanguage(filePath string) (*types.Language, error) {
	ext := filepath.Ext(filePath)
	language, err := b.GetConfig().FindExtLanguage(ext)

	if err != nil {
		return nil, err
	}

	return language, nil
}

// GetConfig returns the bridge's configuration
func (b *MCPLSPBridge) GetConfig() types.LSPServerConfigProvider {
	return b.config
}

// GetServer returns the bridge's MCP server
func (b *MCPLSPBridge) GetServer() *server.MCPServer {
	return b.server
}

// SetServer sets the bridge's MCP server
func (b *MCPLSPBridge) SetServer(mcpServer *server.MCPServer) {
	b.server = mcpServer
}

// Detects all languages used in a project directory
func (b *MCPLSPBridge) DetectProjectLanguages(projectPath string) ([]types.Language, error) {
	if b.config == nil {
		return nil, errors.New("no LSP configuration available")
	}

	return b.GetConfig().DetectProjectLanguages(projectPath)
}

// Detects the primary language of a project
func (b *MCPLSPBridge) DetectPrimaryProjectLanguage(projectPath string) (*types.Language, error) {
	if b.config == nil {
		return nil, errors.New("no LSP configuration available")
	}

	return b.GetConfig().DetectPrimaryProjectLanguage(projectPath)
}

// deduplicateLocations removes duplicate locations from a slice
// Two locations are considered equal if they have the same URI and Range
func deduplicateLocations(locations []protocol.Location) []protocol.Location {
	if len(locations) == 0 {
		return locations
	}

	// Use a map with key = "uri:startLine:startChar:endLine:endChar"
	seen := make(map[string]bool)
	result := make([]protocol.Location, 0, len(locations))
	duplicateCount := 0

	for _, loc := range locations {
		key := fmt.Sprintf("%s:%d:%d:%d:%d",
			loc.Uri,
			loc.Range.Start.Line,
			loc.Range.Start.Character,
			loc.Range.End.Line,
			loc.Range.End.Character)

		if !seen[key] {
			seen[key] = true
			result = append(result, loc)
		} else {
			duplicateCount++
		}
	}

	if duplicateCount > 0 {
		logger.Debug(fmt.Sprintf("Removed %d duplicate location(s), %d unique locations remain", duplicateCount, len(result)))
	}

	return result
}

// deduplicateDocumentSymbols removes duplicate document symbols from a slice
// Handles hierarchical structure with recursive deduplication of children
// Deduplicates by name + range at each level
func deduplicateDocumentSymbols(symbols []protocol.DocumentSymbol) []protocol.DocumentSymbol {
	if len(symbols) == 0 {
		return symbols
	}

	// Use a map with key = "name:startLine:startChar:endLine:endChar"
	seen := make(map[string]bool)
	result := make([]protocol.DocumentSymbol, 0, len(symbols))
	duplicateCount := 0

	for _, symbol := range symbols {
		key := fmt.Sprintf("%s:%d:%d:%d:%d",
			symbol.Name,
			symbol.Range.Start.Line,
			symbol.Range.Start.Character,
			symbol.Range.End.Line,
			symbol.Range.End.Character)

		if !seen[key] {
			seen[key] = true
			// Recursively deduplicate children if they exist
			if len(symbol.Children) > 0 {
				symbol.Children = deduplicateDocumentSymbols(symbol.Children)
			}
			result = append(result, symbol)
		} else {
			duplicateCount++
		}
	}

	if duplicateCount > 0 {
		logger.Debug(fmt.Sprintf("Removed %d duplicate document symbol(s), %d unique symbols remain", duplicateCount, len(result)))
	}

	return result
}

// deduplicateDefinitions removes duplicate definitions from a slice
// Handles both LocationLink and Location types in the Or2 union
func deduplicateDefinitions(definitions []protocol.Or2[protocol.LocationLink, protocol.Location]) []protocol.Or2[protocol.LocationLink, protocol.Location] {
	if len(definitions) == 0 {
		return definitions
	}

	// Use a map with key = "uri:startLine:startChar:endLine:endChar"
	seen := make(map[string]bool)
	result := make([]protocol.Or2[protocol.LocationLink, protocol.Location], 0, len(definitions))
	duplicateCount := 0

	for _, def := range definitions {
		var key string

		// Extract the URI and Range based on the type
		if locLink, ok := def.Value.(protocol.LocationLink); ok {
			// For LocationLink, use TargetUri and TargetRange
			key = fmt.Sprintf("%s:%d:%d:%d:%d",
				locLink.TargetUri,
				locLink.TargetRange.Start.Line,
				locLink.TargetRange.Start.Character,
				locLink.TargetRange.End.Line,
				locLink.TargetRange.End.Character)
		} else if loc, ok := def.Value.(protocol.Location); ok {
			// For Location, use Uri and Range
			key = fmt.Sprintf("%s:%d:%d:%d:%d",
				loc.Uri,
				loc.Range.Start.Line,
				loc.Range.Start.Character,
				loc.Range.End.Line,
				loc.Range.End.Character)
		} else {
			// Unknown type, skip it
			logger.Warn(fmt.Sprintf("Unknown definition type: %T", def.Value))
			continue
		}

		if !seen[key] {
			seen[key] = true
			result = append(result, def)
		} else {
			duplicateCount++
		}
	}

	if duplicateCount > 0 {
		logger.Debug(fmt.Sprintf("Removed %d duplicate definition(s), %d unique definitions remain", duplicateCount, len(result)))
	}

	return result
}

// Finds all references to a symbol at a given position
// Queries all capable language servers and deduplicates results by location
func (b *MCPLSPBridge) FindSymbolReferences(language, uri string, line, character uint32, includeDeclaration bool) ([]protocol.Location, error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("FindSymbolReferences: Starting request for URI: %s -> %s at %d:%d", uri, normalizedURI, line, character))

	// Get ALL clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(language)
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", language, err)
	}
	logger.Info(fmt.Sprintf("FindSymbolReferences: Querying %d language server(s) for %s: %v",
		len(clients), language, serverNames))

	// Aggregate references from all capable servers
	var allReferences []protocol.Location

	for i, client := range clients {
		serverName := serverNames[i]
		logger.Debug(fmt.Sprintf("Processing references for %s (server %d/%d)", serverName, i+1, len(clients)))

		// Check if server supports references
		serverCaps := client.ServerCapabilities()
		if serverCaps.ReferencesProvider == nil {
			logger.Debug(fmt.Sprintf("Server %s does not support references, skipping", serverName))
			continue
		}

		// Ensure document is open
		err = b.ensureDocumentOpen(client, normalizedURI, language)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to open document in %s: %v", serverName, err))
		}

		// Query this server for references
		references, err := client.References(normalizedURI, line, character, includeDeclaration)
		if err != nil {
			logger.Warn(fmt.Sprintf("References failed for %s: %v", serverName, err))
			continue
		}

		logger.Info(fmt.Sprintf("Retrieved %d reference(s) from %s", len(references), serverName))
		allReferences = append(allReferences, references...)
	}

	// Deduplicate by location
	deduplicated := deduplicateLocations(allReferences)

	logger.Info(fmt.Sprintf("FindSymbolReferences: Returning %d total reference(s) from %d server(s) (after deduplication)",
		len(deduplicated), len(clients)))

	return deduplicated, nil
}

// FindSymbolDefinitions finds all definitions for a symbol at a given position
// Queries all capable language servers and deduplicates results by location
func (b *MCPLSPBridge) FindSymbolDefinitions(language, uri string, line, character uint32) ([]protocol.Or2[protocol.LocationLink, protocol.Location], error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("FindSymbolDefinitions: Starting request for URI: %s -> %s at %d:%d", uri, normalizedURI, line, character))

	// Get ALL clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(language)
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", language, err)
	}
	logger.Info(fmt.Sprintf("FindSymbolDefinitions: Querying %d language server(s) for %s: %v",
		len(clients), language, serverNames))

	// Aggregate definitions from all capable servers
	var allDefinitions []protocol.Or2[protocol.LocationLink, protocol.Location]

	for i, client := range clients {
		serverName := serverNames[i]
		logger.Debug(fmt.Sprintf("Processing definitions for %s (server %d/%d)", serverName, i+1, len(clients)))

		// Check if server supports definitions
		serverCaps := client.ServerCapabilities()
		if serverCaps.DefinitionProvider == nil {
			logger.Debug(fmt.Sprintf("Server %s does not support definitions, skipping", serverName))
			continue
		}

		// Ensure document is open
		err = b.ensureDocumentOpen(client, normalizedURI, language)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to open document in %s: %v", serverName, err))
		}

		// Query this server for definitions
		definitions, err := client.Definition(normalizedURI, line, character)
		if err != nil {
			logger.Warn(fmt.Sprintf("Definitions failed for %s: %v", serverName, err))
			continue
		}

		logger.Info(fmt.Sprintf("Retrieved %d definition(s) from %s", len(definitions), serverName))
		allDefinitions = append(allDefinitions, definitions...)
	}

	// Deduplicate by location
	deduplicated := deduplicateDefinitions(allDefinitions)

	logger.Info(fmt.Sprintf("FindSymbolDefinitions: Returning %d total definition(s) from %d server(s) (after deduplication)",
		len(deduplicated), len(clients)))

	return deduplicated, nil
}

// SearchTextInWorkspace performs a text search across the workspace
// Queries all capable language servers for the language and aggregates results
func (b *MCPLSPBridge) SearchTextInWorkspace(language, query string) ([]protocol.WorkspaceSymbol, error) {
	// Get ALL clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(language)
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", language, err)
	}

	logger.Info(fmt.Sprintf("SearchTextInWorkspace: Querying %d language server(s) for %s: %v",
		len(clients), language, serverNames))

	var allSymbols []protocol.WorkspaceSymbol
	// Map to deduplicate symbols by name + location (URI + Range)
	symbolMap := make(map[string]protocol.WorkspaceSymbol)

	for i, client := range clients {
		serverName := serverNames[i]

		// Check if server supports workspace symbols
		serverCaps := client.ServerCapabilities()
		if serverCaps.WorkspaceSymbolProvider == nil {
			logger.Debug(fmt.Sprintf("Server %s does not support workspace symbols", serverName))
			continue
		}

		logger.Debug(fmt.Sprintf("Querying workspace symbols from %s", serverName))

		symbols, err := client.WorkspaceSymbols(query)
		if err != nil {
			logger.Warn(fmt.Sprintf("Workspace symbol search failed for %s: %v", serverName, err))
			continue
		}

		logger.Info(fmt.Sprintf("Retrieved %d workspace symbols from %s", len(symbols), serverName))

		// Deduplicate symbols by name + location
		for _, symbol := range symbols {
			// Create a unique key based on name and location
			var locationKey string
			if symbol.Location.Value != nil {
				if loc, ok := symbol.Location.Value.(protocol.Location); ok {
					locationKey = fmt.Sprintf("%s|%s|%d:%d-%d:%d",
						symbol.Name, loc.Uri,
						loc.Range.Start.Line, loc.Range.Start.Character,
						loc.Range.End.Line, loc.Range.End.Character)
				} else if wsLoc, ok := symbol.Location.Value.(protocol.LocationUriOnly); ok {
					// Handle LocationUriOnly case
					locationKey = fmt.Sprintf("%s|%s", symbol.Name, wsLoc.Uri)
				} else {
					// Fallback: just use name if location type is unknown
					locationKey = symbol.Name
				}
			} else {
				locationKey = symbol.Name
			}

			// Keep the first occurrence (or replace if you prefer later ones)
			if _, exists := symbolMap[locationKey]; !exists {
				symbolMap[locationKey] = symbol
			}
		}
	}

	// Convert map back to slice
	for _, symbol := range symbolMap {
		allSymbols = append(allSymbols, symbol)
	}

	logger.Info(fmt.Sprintf("SearchTextInWorkspace: Returning %d deduplicated symbols from %d server(s)",
		len(allSymbols), len(clients)))

	return allSymbols, nil
}

// SearchTextInAllLanguages performs a text search across all connected language clients
func (b *MCPLSPBridge) SearchTextInAllLanguages(query string) ([]protocol.WorkspaceSymbol, error) {
	var allSymbols []protocol.WorkspaceSymbol
	var errs []error

	// Get all currently connected clients
	b.mu.RLock()
	clientMap := make(map[types.LanguageServer]types.LanguageClientInterface)
	for server, client := range b.clients {
		// Check if client context is still valid
		if client.Context().Err() == nil {
			clientMap[server] = client
		}
	}
	b.mu.RUnlock()

	if len(clientMap) == 0 {
		return nil, errors.New("no connected language clients available for search")
	}

	// Search across all connected clients
	for server, client := range clientMap {
		symbols, err := client.WorkspaceSymbols(query)
		if err != nil {
			errs = append(errs, fmt.Errorf("search failed for %s: %w", server, err))
			continue
		}
		allSymbols = append(allSymbols, symbols...)
	}

	// If all searches failed, return the combined error
	if len(allSymbols) == 0 && len(errs) > 0 {
		var errMsg strings.Builder
		errMsg.WriteString("all language client searches failed: ")
		for i, err := range errs {
			if i > 0 {
				errMsg.WriteString("; ")
			}
			errMsg.WriteString(err.Error())
		}
		return nil, fmt.Errorf("%s", errMsg.String())
	}

	return allSymbols, nil
}

// GetMultiLanguageClients gets language clients for multiple languages with fallback
func (b *MCPLSPBridge) GetMultiLanguageClients(languages []string) (map[types.Language]types.LanguageClientInterface, error) {
	clients := make(map[types.Language]types.LanguageClientInterface)

	var mu sync.Mutex

	var wg sync.WaitGroup

	var lastErr error

	var errMu sync.Mutex

	for _, language := range languages {
		wg.Add(1)

		go func(lang string) {
			defer wg.Done()

			client, err := b.GetClientForLanguage(lang)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to get client for language %s: %v", lang, err))
				errMu.Lock()
				lastErr = err
				errMu.Unlock()

				return
			}

			mu.Lock()
			clients[types.Language(lang)] = client
			mu.Unlock()
		}(language)
	}

	wg.Wait()

	if len(clients) == 0 && lastErr != nil {
		return nil, fmt.Errorf("failed to get any language clients: %w", lastErr)
	}

	return clients, nil
}

// GetHoverInformation gets hover information for a symbol at a specific position
// Queries all capable language servers and aggregates their hover results
func (b *MCPLSPBridge) GetHoverInformation(uri string, line, character uint32) (*protocol.Hover, error) {
	// Extensive debug logging
	logger.Debug(fmt.Sprintf("GetHoverInformation: Starting hover request for URI: %s, Line: %d, Character: %d", uri, line, character))

	// Normalize URI to ensure proper file:// scheme
	normalizedURI := utils.NormalizeURI(uri)

	// Infer language from URI (use original URI for file extension detection)
	language, err := b.InferLanguage(uri)
	if err != nil {
		logger.Error("GetHoverInformation: Failed to infer language", fmt.Sprintf("URI: %s, Error: %v", uri, err))
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	logger.Debug(fmt.Sprintf("GetHoverInformation: Inferred language: %s", string(*language)))

	// Query all servers that support hover capability
	results, err := b.queryAllCapableServers(
		string(*language),
		normalizedURI,
		func(serverCaps *protocol.ServerCapabilities) bool {
			return serverCaps.HoverProvider != nil
		},
		func(client types.LanguageClientInterface) (interface{}, error) {
			return client.Hover(normalizedURI, line, character)
		},
	)

	if err != nil {
		logger.Error("GetHoverInformation: Failed to query servers", fmt.Sprintf("Language: %s, Error: %v", string(*language), err))
		return nil, fmt.Errorf("failed to query hover from servers: %w", err)
	}

	// Aggregate hover results from all servers
	var hoverContents []string

	for _, result := range results {
		if result.Error != nil {
			logger.Warn(fmt.Sprintf("Hover query failed for %s: %v", result.ServerName, result.Error))
			continue
		}

		if result.Result == nil {
			logger.Debug(fmt.Sprintf("No hover info from %s", result.ServerName))
			continue
		}

		// Type assert to *protocol.Hover
		hover, ok := result.Result.(*protocol.Hover)
		if !ok {
			logger.Warn(fmt.Sprintf("Unexpected hover result type from %s: %T", result.ServerName, result.Result))
			continue
		}

		// Extract content from hover
		content := extractHoverContent(hover)
		if content == "" {
			logger.Debug(fmt.Sprintf("Empty hover content from %s", result.ServerName))
			continue
		}

		// Format with server name header
		formattedContent := fmt.Sprintf("### %s\n\n%s", result.ServerName, content)
		hoverContents = append(hoverContents, formattedContent)

		logger.Info(fmt.Sprintf("Retrieved hover info from %s (%d chars)", result.ServerName, len(content)))
	}

	// If no hover info was found, return nil
	if len(hoverContents) == 0 {
		logger.Debug("GetHoverInformation: No hover info available from any server")
		return nil, nil
	}

	// Combine all hover contents into a single markdown string
	combinedContent := strings.Join(hoverContents, "\n\n---\n\n")

	// Create aggregated hover result
	aggregatedHover := &protocol.Hover{
		Contents: protocol.Or3[protocol.MarkupContent, protocol.MarkedString, []protocol.MarkedString]{
			Value: protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: combinedContent,
			},
		},
	}

	logger.Info(fmt.Sprintf("GetHoverInformation: Returning aggregated hover from %d server(s)", len(hoverContents)))
	return aggregatedHover, nil
}

// extractHoverContent extracts text content from a Hover result
// Handles Or3[MarkupContent, MarkedString, []MarkedString] union type
func extractHoverContent(hover *protocol.Hover) string {
	if hover == nil {
		return ""
	}

	// The Contents field is Or3[MarkupContent, MarkedString, []MarkedString]
	// Try to extract content based on the actual type
	switch v := hover.Contents.Value.(type) {
	case protocol.MarkupContent:
		// MarkupContent has a Value field with the actual content
		return v.Value

	case protocol.MarkedString:
		// MarkedString is Or2[string, MarkedStringWithLanguage]
		switch ms := v.Value.(type) {
		case string:
			return ms
		case protocol.MarkedStringWithLanguage:
			// MarkedStringWithLanguage has Language and Value fields
			// Format as a code block
			return fmt.Sprintf("```%s\n%s\n```", ms.Language, ms.Value)
		default:
			logger.Warn(fmt.Sprintf("Unknown MarkedString type: %T", ms))
			return ""
		}

	case []protocol.MarkedString:
		// Array of MarkedString - extract and join
		var parts []string
		for _, ms := range v {
			switch msVal := ms.Value.(type) {
			case string:
				parts = append(parts, msVal)
			case protocol.MarkedStringWithLanguage:
				parts = append(parts, fmt.Sprintf("```%s\n%s\n```", msVal.Language, msVal.Value))
			default:
				logger.Warn(fmt.Sprintf("Unknown MarkedString array element type: %T", msVal))
			}
		}
		return strings.Join(parts, "\n\n")

	default:
		logger.Warn(fmt.Sprintf("Unknown hover Contents type: %T", v))
		return ""
	}
}

// ensureDocumentOpen sends a textDocument/didOpen notification to the language server
// This is often required before other document operations can be performed
// Now with duplicate detection - skips if document is already open
func (b *MCPLSPBridge) ensureDocumentOpen(client types.LanguageClientInterface, uri, language string) error {
	// Check if document is already open (type assert to get IsDocumentOpen method)
	if lspClient, ok := client.(*lsp.LanguageClient); ok {
		if lspClient.IsDocumentOpen(uri) {
			logger.Debug(fmt.Sprintf("Document already open, skipping didOpen: %s", uri))
			return nil
		}
	}

	// Read the file content
	// Remove file:// prefix to get the actual file path
	filePath := strings.TrimPrefix(uri, "file://")

	projectRoots := client.ProjectRoots()

	// Clean the path to resolve .. and . elements
	cleanPath := filepath.Clean(filePath)

	// Convert to absolute path
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}

	for _, allowedBaseDir := range projectRoots {
		// Validate against allowed base directory
		if !security.IsWithinAllowedDirectory(absPath, allowedBaseDir) {
			return errors.New("access denied: path outside allowed directory")
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	// Use DidOpen method which now has duplicate detection built in
	err = client.DidOpen(uri, protocol.LanguageKind(language), string(content), 1)
	if err != nil {
		return fmt.Errorf("failed to send didOpen notification: %w", err)
	}

	logger.Debug(fmt.Sprintf("Document opened in LSP server: %s (language: %s)", uri, language))

	return nil
}

// GetDocumentSymbols gets all symbols in a document
// Queries all capable language servers and deduplicates hierarchical symbol results
func (b *MCPLSPBridge) GetDocumentSymbols(uri string) ([]protocol.DocumentSymbol, error) {
	// Normalize URI to ensure proper file:// scheme
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("GetDocumentSymbols: Starting request for URI: %s -> %s", uri, normalizedURI))

	// Infer language from URI
	language, err := b.InferLanguage(uri)
	if err != nil {
		logger.Error("GetDocumentSymbols: Failed to infer language", fmt.Sprintf("URI: %s, Error: %v", uri, err))
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	logger.Debug(fmt.Sprintf("GetDocumentSymbols: Inferred language: %s", *language))

	// Get ALL clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(string(*language))
	if err != nil {
		logger.Error("GetDocumentSymbols: Failed to get language clients", fmt.Sprintf("Language: %s, Error: %v", string(*language), err))
		return nil, fmt.Errorf("failed to get clients for language %s: %w", string(*language), err)
	}

	logger.Info(fmt.Sprintf("GetDocumentSymbols: Querying %d language server(s) for %s: %v",
		len(clients), string(*language), serverNames))

	// Aggregate symbols from all capable servers
	var allSymbols []protocol.DocumentSymbol

	for i, client := range clients {
		serverName := serverNames[i]
		logger.Debug(fmt.Sprintf("Processing document symbols for %s (server %d/%d)", serverName, i+1, len(clients)))

		// Check if server supports document symbols
		serverCaps := client.ServerCapabilities()
		if serverCaps.DocumentSymbolProvider == nil {
			logger.Debug(fmt.Sprintf("Server %s does not support document symbols, skipping", serverName))
			continue
		}

		// Ensure document is open
		err = b.ensureDocumentOpen(client, normalizedURI, string(*language))
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to open document in %s: %v", serverName, err))
		}

		// Query this server for document symbols
		symbols, err := client.DocumentSymbols(normalizedURI)
		if err != nil {
			logger.Warn(fmt.Sprintf("Document symbols failed for %s: %v", serverName, err))
			continue
		}

		logger.Info(fmt.Sprintf("Retrieved %d document symbol(s) from %s", len(symbols), serverName))
		allSymbols = append(allSymbols, symbols...)
	}

	// Deduplicate hierarchical symbols by name + range
	deduplicated := deduplicateDocumentSymbols(allSymbols)

	logger.Info(fmt.Sprintf("GetDocumentSymbols: Returning %d total symbol(s) from %d server(s) (after deduplication)",
		len(deduplicated), len(clients)))

	return deduplicated, nil
}

func (b *MCPLSPBridge) GetServerConfig(language string) (types.LanguageServerConfigProvider, error) {
	languageServer, err := b.GetConfig().FindServerConfig(language)
	if err != nil {
		return nil, err
	}

	return languageServer, nil
}

// GetSignatureHelp gets signature help for a function at a specific position
// Queries all capable language servers and aggregates their signature information
func (b *MCPLSPBridge) GetSignatureHelp(uri string, line, character uint32) (*protocol.SignatureHelp, error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("GetSignatureHelp: Starting request for URI: %s -> %s at %d:%d", uri, normalizedURI, line, character))

	// Infer language from URI
	language, err := b.InferLanguage(uri)
	if err != nil {
		logger.Error("GetSignatureHelp: Failed to infer language", fmt.Sprintf("URI: %s, Error: %v", uri, err))
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	logger.Debug(fmt.Sprintf("GetSignatureHelp: Inferred language: %s", string(*language)))

	// Query all servers that support signature help capability
	results, err := b.queryAllCapableServers(
		string(*language),
		normalizedURI,
		func(serverCaps *protocol.ServerCapabilities) bool {
			return serverCaps.SignatureHelpProvider != nil
		},
		func(client types.LanguageClientInterface) (interface{}, error) {
			return client.SignatureHelp(normalizedURI, line, character)
		},
	)

	if err != nil {
		logger.Error("GetSignatureHelp: Failed to query servers", fmt.Sprintf("Language: %s, Error: %v", string(*language), err))
		return nil, fmt.Errorf("failed to query signature help from servers: %w", err)
	}

	// Aggregate signature help results from all servers
	var allSignatures []protocol.SignatureInformation
	var activeSignature *uint32
	var activeParameter *uint32
	seenSignatures := make(map[string]bool) // Deduplicate by label

	for _, result := range results {
		if result.Error != nil {
			logger.Warn(fmt.Sprintf("Signature help query failed for %s: %v", result.ServerName, result.Error))
			continue
		}

		if result.Result == nil {
			logger.Debug(fmt.Sprintf("No signature help from %s", result.ServerName))
			continue
		}

		// Type assert to *protocol.SignatureHelp
		sigHelp, ok := result.Result.(*protocol.SignatureHelp)
		if !ok {
			logger.Warn(fmt.Sprintf("Unexpected signature help result type from %s: %T", result.ServerName, result.Result))
			continue
		}

		if sigHelp == nil || len(sigHelp.Signatures) == 0 {
			logger.Debug(fmt.Sprintf("No signatures from %s", result.ServerName))
			continue
		}

		logger.Info(fmt.Sprintf("Retrieved %d signature(s) from %s", len(sigHelp.Signatures), result.ServerName))

		// Collect unique signatures by label
		for _, sig := range sigHelp.Signatures {
			if !seenSignatures[sig.Label] {
				seenSignatures[sig.Label] = true
				allSignatures = append(allSignatures, sig)
				logger.Debug(fmt.Sprintf("Added unique signature: %s", sig.Label))
			} else {
				logger.Debug(fmt.Sprintf("Skipped duplicate signature: %s", sig.Label))
			}
		}

		// Prefer the first non-default activeSignature value
		if activeSignature == nil && sigHelp.ActiveSignature != 0 {
			activeSignature = &sigHelp.ActiveSignature
			logger.Debug(fmt.Sprintf("Using activeSignature from %s: %d", result.ServerName, sigHelp.ActiveSignature))
		}

		// Prefer the first non-nil activeParameter value
		if activeParameter == nil && sigHelp.ActiveParameter != nil {
			activeParameter = sigHelp.ActiveParameter
			logger.Debug(fmt.Sprintf("Using activeParameter from %s: %d", result.ServerName, *sigHelp.ActiveParameter))
		}
	}

	// If no signatures were found from any server, return nil
	if len(allSignatures) == 0 {
		logger.Debug("GetSignatureHelp: No signature help available from any server")
		return nil, nil
	}

	// Build aggregated signature help
	aggregated := &protocol.SignatureHelp{
		Signatures: allSignatures,
	}

	// Set activeSignature if we found one, otherwise use default 0
	if activeSignature != nil {
		aggregated.ActiveSignature = *activeSignature
	} else {
		aggregated.ActiveSignature = 0
	}

	// Set activeParameter if we found one
	if activeParameter != nil {
		aggregated.ActiveParameter = activeParameter
	}

	logger.Info(fmt.Sprintf("GetSignatureHelp: Returning %d unique signature(s) from multiple servers (activeSignature: %d, activeParameter: %v)",
		len(allSignatures), aggregated.ActiveSignature, activeParameter))

	return aggregated, nil
}

// GetCodeActions gets code actions for a specific range
func (b *MCPLSPBridge) GetCodeActions(uri string, line, character, endLine, endCharacter uint32) ([]protocol.CodeAction, error) {
	// Normalize URI to ensure proper file:// scheme
	normalizedURI := utils.NormalizeURI(uri)

	// Infer language from URI
	language, err := b.InferLanguage(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	// Get all clients for this language
	clients, serverNames, err := b.GetAllClientsForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", string(*language), err)
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("no language servers available for language %s", string(*language))
	}

	var allCodeActions []protocol.CodeAction
	var errors []string

	// Query all servers that support code actions
	for i, client := range clients {
		serverName := serverNames[i]

		// Check if server supports code actions
		serverCaps := client.ServerCapabilities()
		if serverCaps.CodeActionProvider == nil {
			logger.Debug(fmt.Sprintf("Server %s does not support code actions, skipping", serverName))
			continue
		}

		logger.Debug(fmt.Sprintf("Querying code actions from server %s", serverName))

		// Ensure document is open for this server
		if err := b.ensureDocumentOpen(client, normalizedURI, string(*language)); err != nil {
			logger.Warn(fmt.Sprintf("Failed to ensure document open for %s: %v", serverName, err))
			errors = append(errors, fmt.Sprintf("%s: document open failed: %v", serverName, err))
			continue
		}

		// Query code actions from this server
		codeActions, err := client.CodeActions(normalizedURI, line, character, endLine, endCharacter)
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to get code actions from %s: %v", serverName, err))
			errors = append(errors, fmt.Sprintf("%s: %v", serverName, err))
			continue
		}

		if len(codeActions) > 0 {
			logger.Debug(fmt.Sprintf("Server %s provided %d code action(s)", serverName, len(codeActions)))

			// Add server name prefix to each action title for clarity
			for j := range codeActions {
				if codeActions[j].Title != "" {
					codeActions[j].Title = fmt.Sprintf("[%s] %s", serverName, codeActions[j].Title)
				}
			}

			allCodeActions = append(allCodeActions, codeActions...)
		} else {
			logger.Debug(fmt.Sprintf("Server %s returned no code actions", serverName))
		}
	}

	// If we got no code actions from any server and had errors, return an error
	if len(allCodeActions) == 0 && len(errors) > 0 {
		return nil, fmt.Errorf("all code action requests failed: %s", strings.Join(errors, "; "))
	}

	logger.Debug(fmt.Sprintf("GetCodeActions returning %d total code action(s) from %d server(s)", len(allCodeActions), len(clients)))
	return allCodeActions, nil
}

// FormatDocument formats a document
func (b *MCPLSPBridge) FormatDocument(uri string, tabSize uint32, insertSpaces bool) ([]protocol.TextEdit, error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)

	// Infer language from URI
	language, err := b.InferLanguage(normalizedURI)
	if err != nil {
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	logger.Debug(fmt.Sprintf("FormatDocument: Language detected: %s", string(*language)))

	// Check for preferred formatter in config
	var preferredFormatter types.LanguageServer
	if lspConfig, ok := b.config.(*lsp.LSPServerConfig); ok {
		preferredFormatter = lspConfig.GetPreferredFormatter(string(*language))
	}

	// If a preferred formatter is configured, use only that server
	if preferredFormatter != "" {
		logger.Info(fmt.Sprintf("FormatDocument: Using preferred formatter: %s", preferredFormatter))

		// Get the specific client - check if it exists first
		client, exists := b.clients[preferredFormatter]
		if !exists {
			// Try to create the client - need to find its config
			serverConfigs, serverNames, err := b.config.FindAllServerConfigs(string(*language))
			if err != nil {
				return nil, fmt.Errorf("no servers found for language %s: %w", string(*language), err)
			}

			// Find the preferred formatter's config
			var preferredConfig types.LanguageServerConfigProvider
			for i, serverName := range serverNames {
				if serverName == preferredFormatter {
					preferredConfig = serverConfigs[i]
					break
				}
			}

			if preferredConfig == nil {
				return nil, fmt.Errorf("preferred formatter %s not configured for language %s", preferredFormatter, string(*language))
			}

			client, err = b.validateAndConnectClient(string(*language), preferredConfig, DefaultConnectionConfig())
			if err != nil {
				return nil, fmt.Errorf("failed to connect to preferred formatter %s: %w", preferredFormatter, err)
			}

			b.clients[preferredFormatter] = client
		}

		// Check if server supports formatting
		serverCaps := client.ServerCapabilities()
		if serverCaps.DocumentFormattingProvider == nil {
			return nil, fmt.Errorf("preferred formatter %s does not support document formatting", preferredFormatter)
		}

		// Execute formatting request
		result, err := client.Formatting(normalizedURI, tabSize, insertSpaces)
		if err != nil {
			return nil, fmt.Errorf("document formatting with %s failed: %w", preferredFormatter, err)
		}

		logger.Info(fmt.Sprintf("FormatDocument: Successfully formatted with %s (%d edits)", preferredFormatter, len(result)))
		return result, nil
	}

	// No preferred formatter - query all capable servers
	logger.Debug("FormatDocument: No preferred formatter configured, querying all capable servers")

	// Get all clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", string(*language), err)
	}

	logger.Debug(fmt.Sprintf("FormatDocument: Found %d server(s) for %s: %v", len(clients), string(*language), serverNames))

	// Query all servers that support formatting
	type formatterResult struct {
		serverName types.LanguageServer
		edits      []protocol.TextEdit
	}

	var results []formatterResult

	for i, client := range clients {
		serverName := serverNames[i]

		// Check if server supports formatting
		serverCaps := client.ServerCapabilities()
		if serverCaps.DocumentFormattingProvider == nil {
			logger.Debug(fmt.Sprintf("FormatDocument: Server %s does not support document formatting", serverName))
			continue
		}

		logger.Debug(fmt.Sprintf("FormatDocument: Querying formatter %s", serverName))

		// Execute formatting request
		edits, err := client.Formatting(normalizedURI, tabSize, insertSpaces)
		if err != nil {
			logger.Warn(fmt.Sprintf("FormatDocument: Formatter %s failed: %v", serverName, err))
			continue
		}

		logger.Debug(fmt.Sprintf("FormatDocument: Formatter %s returned %d edits", serverName, len(edits)))

		results = append(results, formatterResult{
			serverName: serverName,
			edits:      edits,
		})
	}

	// Handle results
	if len(results) == 0 {
		return nil, fmt.Errorf("no formatters available for language %s", string(*language))
	}

	if len(results) == 1 {
		logger.Info(fmt.Sprintf("FormatDocument: Using formatter %s (%d edits)", results[0].serverName, len(results[0].edits)))
		return results[0].edits, nil
	}

	// Multiple formatters available - return first result with note
	var formatterNames []string
	for _, r := range results {
		formatterNames = append(formatterNames, string(r.serverName))
	}

	logger.Warn(fmt.Sprintf("FormatDocument: Multiple formatters available: %v. Using first: %s. Configure preferred_formatters in config to specify preference.", formatterNames, results[0].serverName))
	logger.Info(fmt.Sprintf("FormatDocument: Using formatter %s (%d edits) - configure preferred_formatters to specify preference", results[0].serverName, len(results[0].edits)))

	return results[0].edits, nil
}

// ApplyTextEdits applies text edits to a file
func (b *MCPLSPBridge) ApplyTextEdits(uri string, edits []protocol.TextEdit) error {
	// Convert URI to file path
	filePath := strings.TrimPrefix(uri, "file://")
	filePath, err := b.IsAllowedDirectory(filePath)
	// Check if file path is allowed
	if err != nil {
		return fmt.Errorf("file path is not allowed: %s", filePath)
	}

	// Read current file content
	content, err := os.ReadFile(filePath) // #nosec G304
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file stats for %s: %w", filePath, err)
	}

	// Apply edits to content
	modifiedContent, err := applyTextEditsToContent(string(content), edits)
	if err != nil {
		return fmt.Errorf("failed to apply text edits: %w", err)
	}

	// Write modified content back to file
	err = os.WriteFile(filePath, []byte(modifiedContent), stat.Mode())
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", filePath, err)
	}

	return nil
}

// applyTextEditsToContent applies text edits to string content
func applyTextEditsToContent(content string, edits []protocol.TextEdit) (string, error) {
	if len(edits) == 0 {
		return content, nil
	}

	// Split content into lines for easier manipulation. Use "\n" directly in the string
	lines := strings.Split(content, "\n")

	// Sort edits by position (reverse order to apply from end to beginning)
	// This prevents position shifts from affecting subsequent edits
	for i := 0; i < len(edits)-1; i++ {
		for j := i + 1; j < len(edits); j++ {
			edit1 := edits[i]
			edit2 := edits[j]

			// Compare positions (later positions first)
			if edit1.Range.Start.Line < edit2.Range.Start.Line ||
				(edit1.Range.Start.Line == edit2.Range.Start.Line &&
					edit1.Range.Start.Character < edit2.Range.Start.Character) {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}

	// Apply edits in reverse order
	for _, edit := range edits {
		startLine := int(edit.Range.Start.Line)
		startChar := int(edit.Range.Start.Character)
		endLine := int(edit.Range.End.Line)
		endChar := int(edit.Range.End.Character)

		// Validate line indices
		if startLine >= len(lines) || endLine >= len(lines) {
			continue // Skip invalid edits
		}

		if startLine == endLine {
			// Single line edit
			line := lines[startLine]
			if startChar > len(line) || endChar > len(line) {
				continue // Skip invalid character positions
			}

			// Replace text within the line
			newLine := line[:startChar] + edit.NewText + line[endChar:]
			lines[startLine] = newLine
		} else {
			// Multi-line edit
			if startChar > len(lines[startLine]) || endChar > len(lines[endLine]) {
				continue // Skip invalid character positions
			}

			// Create new line combining start of first line + new text + end of last line
			newLine := lines[startLine][:startChar] + edit.NewText + lines[endLine][endChar:]

			// Remove the lines that were replaced
			newLines := make([]string, 0, len(lines)-(endLine-startLine))
			newLines = append(newLines, lines[:startLine]...)
			newLines = append(newLines, newLine)

			if endLine+1 < len(lines) {
				newLines = append(newLines, lines[endLine+1:]...)
			}

			lines = newLines
		}
	}

	// Use "\n" directly in the string
	return strings.Join(lines, "\n"), nil
}

// RenameSymbol renames a symbol with optional preview
func (b *MCPLSPBridge) RenameSymbol(uri string, line, character uint32, newName string, preview bool) (*protocol.WorkspaceEdit, error) {
	// Normalize URI to ensure proper file:// scheme
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("RenameSymbol: Starting rename request for URI: %s -> %s, Line: %d, Character: %d, NewName: %s", uri, normalizedURI, line, character, newName))

	// Infer language from URI
	language, err := b.InferLanguage(uri)
	if err != nil {
		logger.Error("RenameSymbol: Failed to infer language", fmt.Sprintf("URI: %s, Error: %v", uri, err))
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	// Get language client
	client, err := b.GetClientForLanguage(string(*language))
	if err != nil {
		logger.Error("RenameSymbol: Failed to get language client", fmt.Sprintf("Language: %s, Error: %v", string(*language), err))
		return nil, fmt.Errorf("failed to get client for language %s: %w", string(*language), err)
	}

	// Ensure the document is opened in the language server
	err = b.ensureDocumentOpen(client, normalizedURI, string(*language))
	if err != nil {
		// Continue anyway, as some servers might still work without explicit didOpen
		logger.Error("RenameSymbol: Failed to open document", fmt.Sprintf("URI: %s, Error: %v", normalizedURI, err))
	}

	result, err := client.Rename(normalizedURI, line, character, newName)
	if err != nil {
		logger.Error("RenameSymbol: Failed to rename symbol", fmt.Sprintf("URI: %s, Line: %d, Character: %d, NewName: %s, Error: %v", normalizedURI, line, character, newName, err))
		return nil, fmt.Errorf("failed to rename symbol: %w", err)
	}

	return result, nil
}

// ApplyWorkspaceEdit applies a workspace edit to multiple files
func (b *MCPLSPBridge) ApplyWorkspaceEdit(workspaceEdit *protocol.WorkspaceEdit) error {
	logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Processing workspace edit. Changes: %+v, DocumentChanges: %+v", workspaceEdit.Changes, workspaceEdit.DocumentChanges))

	// Handle DocumentChanges format (preferred by most language servers)
	if workspaceEdit.DocumentChanges != nil {
		for _, docChange := range workspaceEdit.DocumentChanges {
			logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Processing document change of type: %T", docChange.Value))

			// DocumentChanges is []Or4[TextDocumentEdit, CreateFile, RenameFile, DeleteFile]
			// We only handle TextDocumentEdit for now
			if textDocEdit, ok := docChange.Value.(protocol.TextDocumentEdit); ok {
				logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Found TextDocumentEdit for URI: %s", textDocEdit.TextDocument.Uri))

				// Convert protocol.TextEdit to []any for ApplyTextEdits
				textEdits := make([]protocol.TextEdit, len(textDocEdit.Edits))

				for i, edit := range textDocEdit.Edits {
					// Edits might also be a union type, extract the actual TextEdit
					if actualEdit, ok := edit.Value.(protocol.TextEdit); ok {
						textEdits[i] = actualEdit
						logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Edit %d - Line %d:%d-%d:%d, NewText: '%s'",
							i+1, actualEdit.Range.Start.Line, actualEdit.Range.Start.Character,
							actualEdit.Range.End.Line, actualEdit.Range.End.Character, actualEdit.NewText))
					} else {
						logger.Error("ApplyWorkspaceEdit: Edit is not a TextEdit", fmt.Sprintf("Type: %T", edit.Value))
						continue
					}
				}

				// Apply the edits
				if len(textEdits) > 0 {
					logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Applying %d text edits to %s", len(textEdits), textDocEdit.TextDocument.Uri))

					err := b.ApplyTextEdits(string(textDocEdit.TextDocument.Uri), textEdits)
					if err != nil {
						return fmt.Errorf("failed to apply document changes to %s: %w", textDocEdit.TextDocument.Uri, err)
					}
				}
			} else if createFile, ok := docChange.Value.(protocol.CreateFile); ok {
				logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Found CreateFile for URI: %s", createFile.Uri))
				filePath := strings.TrimPrefix(string(createFile.Uri), "file://")
				filePath, err := b.IsAllowedDirectory(filePath)
				if err != nil {
					return fmt.Errorf("failed to create file %s: %w", filePath, err)
				}
				// Create the file with default permissions (e.g., 0600)
				err = os.WriteFile(filePath, []byte{}, 0600)
				if err != nil {
					return fmt.Errorf("failed to create file %s: %w", filePath, err)
				}
				logger.Debug("ApplyWorkspaceEdit: Created file " + filePath)
			} else if renameFile, ok := docChange.Value.(protocol.RenameFile); ok {
				logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Found RenameFile from %s to %s", renameFile.OldUri, renameFile.NewUri))
				oldPath := strings.TrimPrefix(string(renameFile.OldUri), "file://")
				newPath := strings.TrimPrefix(string(renameFile.NewUri), "file://")

				oldPath, err := b.IsAllowedDirectory(oldPath)
				if err != nil {
					return fmt.Errorf("failed to rename file (old path not allowed) %s: %w", oldPath, err)
				}
				newPath, err = b.IsAllowedDirectory(newPath)
				if err != nil {
					return fmt.Errorf("failed to rename file (new path not allowed) %s: %w", newPath, err)
				}

				err = os.Rename(oldPath, newPath)
				if err != nil {
					return fmt.Errorf("failed to rename file from %s to %s: %w", oldPath, newPath, err)
				}
				logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Renamed file from %s to %s", oldPath, newPath))
			} else if deleteFile, ok := docChange.Value.(protocol.DeleteFile); ok {
				logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Found DeleteFile for URI: %s", deleteFile.Uri))
				filePath := strings.TrimPrefix(string(deleteFile.Uri), "file://")
				filePath, err := b.IsAllowedDirectory(filePath)
				if err != nil {
					return fmt.Errorf("failed to delete file %s: %w", filePath, err)
				}
				err = os.Remove(filePath)
				if err != nil {
					return fmt.Errorf("failed to delete file %s: %w", filePath, err)
				}
				logger.Debug("ApplyWorkspaceEdit: Deleted file " + filePath)
			} else {
				logger.Debug(fmt.Sprintf("ApplyWorkspaceEdit: Skipping unknown document change type: %T", docChange.Value))
			}
		}
	}

	// Apply changes map (alternative format)
	if workspaceEdit.Changes != nil {
		for uri, edits := range workspaceEdit.Changes {
			err := b.ApplyTextEdits(string(uri), edits)
			if err != nil {
				return fmt.Errorf("failed to apply edits to %s: %w", uri, err)
			}
		}
	}

	return nil
}

// FindImplementations finds implementations of a symbol
// Queries all capable language servers and deduplicates results by location
func (b *MCPLSPBridge) FindImplementations(uri string, line, character uint32) ([]protocol.Location, error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("FindImplementations: Starting request for URI: %s -> %s at %d:%d", uri, normalizedURI, line, character))

	// Infer language from URI
	language, err := b.InferLanguage(normalizedURI)
	if err != nil {
		logger.Error("FindImplementations: Language inference failed", fmt.Sprintf("URI: %s, Error: %v", normalizedURI, err))
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	// Get ALL clients for the language
	clients, serverNames, err := b.GetAllClientsForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", string(*language), err)
	}
	logger.Info(fmt.Sprintf("FindImplementations: Querying %d language server(s) for %s: %v",
		len(clients), string(*language), serverNames))

	// Aggregate implementations from all capable servers
	var allImplementations []protocol.Location

	for i, client := range clients {
		serverName := serverNames[i]
		logger.Debug(fmt.Sprintf("Processing implementations for %s (server %d/%d)", serverName, i+1, len(clients)))

		// Check if server supports implementations
		serverCaps := client.ServerCapabilities()
		if serverCaps.ImplementationProvider == nil {
			logger.Debug(fmt.Sprintf("Server %s does not support implementations, skipping", serverName))
			continue
		}

		// Ensure document is open in the language server
		err = b.ensureDocumentOpen(client, normalizedURI, string(*language))
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to open document in %s: %v", serverName, err))
		}

		// Query this server for implementations
		implementations, err := client.Implementation(normalizedURI, line, character)
		if err != nil {
			logger.Warn(fmt.Sprintf("Implementations failed for %s: %v", serverName, err))
			continue
		}

		logger.Info(fmt.Sprintf("Retrieved %d implementation(s) from %s", len(implementations), serverName))
		allImplementations = append(allImplementations, implementations...)
	}

	// Deduplicate by location
	deduplicated := deduplicateLocations(allImplementations)

	logger.Info(fmt.Sprintf("FindImplementations: Returning %d total implementation(s) from %d server(s) (after deduplication)",
		len(deduplicated), len(clients)))

	return deduplicated, nil
}

func (b *MCPLSPBridge) SemanticTokens(uri string, targetTypes []string, startLine, startCharacter, endLine, endCharacter uint32) ([]types.TokenPosition, error) {
	language, err := b.InferLanguage(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to infer language: %w", err)
	}

	client, err := b.GetClientForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get client for language %s: %w", *language, err)
	}

	err = b.ensureDocumentOpen(client, uri, string(*language))
	if err != nil {
		// Continue anyway, as some servers might still work without explicit didOpen
		logger.Error("SemanticTokens: Failed to open document", fmt.Sprintf("URI: %s, Error: %v", uri, err))
	}

	tokens, err := client.SemanticTokensRange(uri, startLine, startCharacter, endLine, endCharacter)
	if err != nil {
		logger.Error(fmt.Sprintf("SemanticTokens: Failed to get raw semantic tokens from client: %v", err))
		serverCommand := client.GetMetrics().GetCommand()
		return nil, fmt.Errorf("semantic tokens not supported by %s language server for %s files: %w", serverCommand, *language, err)
	}

	// Handle nil tokens response (server returned null)
	if tokens == nil {
		logger.Debug("SemanticTokens: Server returned null/no semantic tokens for this range")
		return []types.TokenPosition{}, nil
	}

	logger.Debug(fmt.Sprintf("SemanticTokens: Raw tokens from LSP client: %+v", tokens))

	logger.Debug("SemanticTokens: About to get token parser")
	parser := client.TokenParser()
	logger.Debug(fmt.Sprintf("SemanticTokens: Got token parser: %v", parser != nil))

	if parser == nil {
		// If no token parser exists but the LSP request succeeded,
		// the server supports semantic tokens but didn't advertise capabilities properly.
		// This is common with some language servers like gopls.
		serverCommand := client.GetMetrics().GetCommand()
		logger.Debug(fmt.Sprintf("SemanticTokens: %s server for %s files supports semantic tokens but didn't advertise capabilities - creating fallback parser", serverCommand, *language))

		// Create a fallback parser with common token types
		fallbackTokenTypes := []string{
			"keyword", "class", "interface", "enum", "function", "method", "macro", "variable",
			"parameter", "property", "label", "comment", "string", "number", "regexp",
			"operator", "decorator", "type", "typeParameter", "namespace", "struct",
			"event", "operator", "modifier", "punctuation", "bracket", "delimiter",
		}
		fallbackTokenModifiers := []string{
			"declaration", "definition", "readonly", "static", "deprecated", "abstract",
			"async", "modification", "documentation", "defaultLibrary",
		}

		// Use the semantic token parser constructor directly
		parser = lsp.NewSemanticTokenParser(fallbackTokenTypes, fallbackTokenModifiers)

		if parser == nil {
			return nil, errors.New("failed to create fallback token parser")
		}

		logger.Debug("SemanticTokens: Created fallback token parser successfully")
	}

	tokenRange := protocol.Range{
		Start: protocol.Position{
			Line:      startLine,
			Character: startCharacter,
		},
		End: protocol.Position{
			Line:      endLine,
			Character: endCharacter,
		},
	}

	// []string{"type", "class", "interface", "struct"}
	positions, err := parser.FindTokensByType(tokens, targetTypes, tokenRange)

	if err != nil {
		return nil, fmt.Errorf("failed to find tokens by types (%v): %w", targetTypes, err)
	}

	return positions, nil
}

// PrepareCallHierarchy prepares call hierarchy items
func (b *MCPLSPBridge) PrepareCallHierarchy(uri string, line, character uint32) ([]protocol.CallHierarchyItem, error) {
	// Infer language from URI
	language, err := b.InferLanguage(uri)
	if err != nil {
		return nil, err
	}

	// Get language client
	client, err := b.GetClientForLanguage(string(*language))
	if err != nil {
		return nil, err
	}

	result, err := client.PrepareCallHierarchy(uri, line, character)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// IncomingCalls gets incoming calls for a call hierarchy item
func (b *MCPLSPBridge) IncomingCalls(item protocol.CallHierarchyItem) ([]protocol.CallHierarchyIncomingCall, error) {
	// Infer language from the URI in the call hierarchy item
	language, err := b.InferLanguage(string(item.Uri))
	if err != nil {
		return nil, fmt.Errorf("failed to infer language from URI %s: %w", item.Uri, err)
	}

	// Get the language client for the inferred language
	client, err := b.GetClientForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get language client for %s: %w", *language, err)
	}

	// Call the language client's IncomingCalls method
	return client.IncomingCalls(item)
}

// OutgoingCalls gets outgoing calls for a call hierarchy item
func (b *MCPLSPBridge) OutgoingCalls(item protocol.CallHierarchyItem) ([]protocol.CallHierarchyOutgoingCall, error) {
	// Infer language from the URI in the call hierarchy item
	language, err := b.InferLanguage(string(item.Uri))
	if err != nil {
		return nil, fmt.Errorf("failed to infer language from URI %s: %w", item.Uri, err)
	}

	// Get the language client for the inferred language
	client, err := b.GetClientForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get language client for %s: %w", *language, err)
	}

	// Call the language client's OutgoingCalls method
	return client.OutgoingCalls(item)
}

// GetWorkspaceDiagnostics gets diagnostics for entire workspace
func (b *MCPLSPBridge) GetWorkspaceDiagnostics(workspaceUri string, identifier string) ([]protocol.WorkspaceDiagnosticReport, error) {
	// 1. Detect project languages or use multi-language approach
	languages, err := b.DetectProjectLanguages(workspaceUri)
	if err != nil {
		return []protocol.WorkspaceDiagnosticReport{}, err
	}

	if len(languages) == 0 {
		return []protocol.WorkspaceDiagnosticReport{}, nil // No languages detected, return empty result
	}

	var languageStrings []string
	for _, lang := range languages {
		languageStrings = append(languageStrings, string(lang))
	}

	// 2. Get language clients for detected languages
	clients, err := b.GetMultiLanguageClients(languageStrings)
	if err != nil {
		return nil, fmt.Errorf("failed to get language clients: %w", err)
	}

	// 3. Execute workspace diagnostic requests
	var allReports []protocol.WorkspaceDiagnosticReport

	for language, clientInterface := range clients {
		client := clientInterface

		report, err := client.WorkspaceDiagnostic(identifier)
		if err != nil {
			logger.Warn(fmt.Sprintf("Workspace diagnostics failed for %s: %v", language, err))
			continue
		}

		allReports = append(allReports, *report)
	}

	return allReports, nil
}

// SetWorkspaceRoots sets the workspace roots from the MCP client
func (b *MCPLSPBridge) SetWorkspaceRoots(paths []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workspaceRoots = paths
	logger.Info(fmt.Sprintf("Updated workspace roots: %v", paths))
}

// GetWorkspaceRoots returns the workspace roots from the MCP client
func (b *MCPLSPBridge) GetWorkspaceRoots() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.workspaceRoots
}

// GetDocumentDiagnostics gets diagnostics for a single document using LSP 3.17+ textDocument/diagnostic method
func (b *MCPLSPBridge) GetDocumentDiagnostics(uri string, identifier string, previousResultId string) (*protocol.DocumentDiagnosticReport, error) {
	// Normalize URI
	normalizedURI := utils.NormalizeURI(uri)
	logger.Debug(fmt.Sprintf("GetDocumentDiagnostics: URI: %s | Normalized: %s", uri, normalizedURI))

	// Determine language from file extension
	language, err := b.InferLanguage(normalizedURI)
	if err != nil {
		return nil, fmt.Errorf("failed to determine language for URI %s: %w", normalizedURI, err)
	}
	logger.Debug(fmt.Sprintf("GetDocumentDiagnostics: Detected language: %s", string(*language)))

	// Get ALL clients for the language (not just first match)
	clients, serverNames, err := b.GetAllClientsForLanguage(string(*language))
	if err != nil {
		return nil, fmt.Errorf("failed to get clients for language %s: %w", string(*language), err)
	}
	logger.Info(fmt.Sprintf("GetDocumentDiagnostics: Querying %d language server(s) for %s: %v",
		len(clients), string(*language), serverNames))

	// Aggregate diagnostics from all servers
	var allDiagnostics []protocol.Diagnostic

	for i, client := range clients {
		serverName := serverNames[i]
		logger.Debug(fmt.Sprintf("Processing diagnostics for %s (server %d/%d)", serverName, i+1, len(clients)))

		// Ensure document is open
		err = b.ensureDocumentOpen(client, normalizedURI, string(*language))
		if err != nil {
			logger.Warn(fmt.Sprintf("Failed to open document in %s: %v - diagnostics may not be available", serverName, err))
			logger.Debug(fmt.Sprintf("Continuing anyway to check if diagnostics are already cached for %s", serverName))
		} else {
			logger.Debug(fmt.Sprintf("Document successfully opened in %s", serverName))
		}

		// Try pull diagnostics first
		report, err := client.DocumentDiagnostics(normalizedURI, identifier, previousResultId)

		if err != nil {
			// Check for unsupported method, fallback to cache
			if strings.Contains(err.Error(), "code -32601") || strings.Contains(err.Error(), "Unhandled method") {
				logger.Info(fmt.Sprintf("Pull diagnostics not supported for %s, falling back to cache", serverName))

				if lc, ok := client.(*lsp.LanguageClient); ok {
					logger.Debug(fmt.Sprintf("Cache lookup for %s with URI: %s", serverName, normalizedURI))
					cached := lc.GetCachedDiagnostics(protocol.DocumentUri(normalizedURI))

					if len(cached) > 0 {
						logger.Info(fmt.Sprintf("✓ Retrieved %d cached diagnostics from %s", len(cached), serverName))
					} else {
						logger.Warn(fmt.Sprintf("✗ No cached diagnostics found for %s (URI: %s)", serverName, normalizedURI))
					}

					allDiagnostics = append(allDiagnostics, cached...)
					continue
				} else {
					logger.Warn(fmt.Sprintf("Client type doesn't support caching for %s", serverName))
				}
			}

			logger.Warn(fmt.Sprintf("Diagnostics failed for %s: %v", serverName, err))
			continue
		}

		// Extract diagnostics from report (Or2 type)
		reportBytes, _ := json.Marshal(report)
		var fullReport protocol.RelatedFullDocumentDiagnosticReport
		if err := json.Unmarshal(reportBytes, &fullReport); err == nil {
			allDiagnostics = append(allDiagnostics, fullReport.Items...)
			logger.Info(fmt.Sprintf("✓ Pull diagnostics succeeded for %s: %d diagnostics", serverName, len(fullReport.Items)))
		} else {
			logger.Warn(fmt.Sprintf("Failed to unmarshal diagnostics report from %s: %v", serverName, err))
		}
	}

	// Construct aggregated report
	aggregatedReport := protocol.RelatedFullDocumentDiagnosticReport{
		Kind:  "full",
		Items: allDiagnostics,
	}

	logger.Info(fmt.Sprintf("GetDocumentDiagnostics: Returning %d total diagnostics from %d server(s)",
		len(allDiagnostics), len(clients)))

	// Wrap in DocumentDiagnosticReport
	reportBytes, _ := json.Marshal(aggregatedReport)
	var docReport protocol.DocumentDiagnosticReport
	json.Unmarshal(reportBytes, &docReport)

	return &docReport, nil
}
