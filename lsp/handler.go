package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/myleshyson/lsprotocol-go/protocol"
	"github.com/sourcegraph/jsonrpc2"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/utils"
)

// ClientHandler handles incoming messages from the language server
type ClientHandler struct {
	// Configuration context for responding to workspace/configuration requests
	client ClientContextProvider
	// Callback function called when diagnostics are received
	diagnosticsCallback func(uri string)
}

// SetDiagnosticsCallback sets a callback to be called when diagnostics are received
func (h *ClientHandler) SetDiagnosticsCallback(callback func(uri string)) {
	h.diagnosticsCallback = callback
}

// ClientContextProvider provides context information for the handler
type ClientContextProvider interface {
	ProjectRoots() []string
	InitializationSettings() map[string]interface{}
	GetWorkspaceConfiguration() map[string]interface{}
}

func (h *ClientHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	switch req.Method {
	case "textDocument/publishDiagnostics":
		// Handle diagnostics - store in cache
		var params protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(*req.Params, &params); err == nil {
			// Type assert to access cache methods
			if client, ok := h.client.(*LanguageClient); ok {
				// Normalize the URI before storing to ensure consistent lookups
				normalizedURI := protocol.DocumentUri(utils.NormalizeURI(string(params.Uri)))

				client.diagnosticMutex.Lock()
				client.diagnosticCache[normalizedURI] = params.Diagnostics
				client.diagnosticMutex.Unlock()

				// Enhanced logging to track URI normalization
				if string(params.Uri) != string(normalizedURI) {
					logger.Debug(fmt.Sprintf("Cached %d diagnostics | Raw URI: %s | Normalized URI: %s",
						len(params.Diagnostics), params.Uri, normalizedURI))
				} else {
					logger.Debug(fmt.Sprintf("Cached %d diagnostics for %s", len(params.Diagnostics), normalizedURI))
				}

				// Notify subscribers via callback
				if h.diagnosticsCallback != nil {
					h.diagnosticsCallback(string(normalizedURI))
				}
			} else {
				logger.Debug(fmt.Sprintf("Diagnostics received but client doesn't support caching: %+v\n", params))
			}
		}

	case "textDocument/didClose":
		// Clean up cached diagnostics for closed files
		var params protocol.DidCloseTextDocumentParams
		if err := json.Unmarshal(*req.Params, &params); err == nil {
			if client, ok := h.client.(*LanguageClient); ok {
				// Normalize URI to match how it was stored in cache
				normalizedURI := protocol.DocumentUri(utils.NormalizeURI(string(params.TextDocument.Uri)))

				client.diagnosticMutex.Lock()
				delete(client.diagnosticCache, normalizedURI)
				client.diagnosticMutex.Unlock()
				logger.Debug(fmt.Sprintf("Cleared diagnostics cache for closed file: %s", normalizedURI))
			}
		}

	case "window/showMessage":
		// Handle show message
		var params any
		if err := json.Unmarshal(*req.Params, &params); err == nil {
			logger.Debug(fmt.Sprintf("Server message: %+v\n", params))
		}

	case "window/logMessage":
		// Handle log message
		var params any
		if err := json.Unmarshal(*req.Params, &params); err == nil {
			logger.Info(fmt.Sprintf("Server log: %+v\n", params))
		}

	case "client/registerCapability":
		// Handle capability registration - reply with success
		if err := conn.Reply(ctx, req.ID, map[string]any{}); err != nil {
			logger.Debug(fmt.Sprintf("Failed to reply to registerCapability: %v\n", err))
		}

	case "workspace/configuration":
		// Handle configuration request
		h.handleWorkspaceConfiguration(ctx, conn, req)

	default:
		logger.Error(fmt.Sprintf("Unhandled method: %s with params: %s", req.Method, string(*req.Params)))

		err := &jsonrpc2.Error{
			Code:    jsonrpc2.CodeMethodNotFound,
			Message: "Method not found",
		}
		if replyErr := conn.ReplyWithError(ctx, req.ID, err); replyErr != nil {
			logger.Error(fmt.Sprintf("Failed to reply with error: %v", replyErr))
		}
	}
}

// ConfigurationItem represents a configuration section to retrieve
type ConfigurationItem struct {
	ScopeURI string `json:"scopeUri,omitempty"`
	Section  string `json:"section,omitempty"`
}

// ConfigurationParams represents the parameters for workspace/configuration request
type ConfigurationParams struct {
	Items []ConfigurationItem `json:"items"`
}

// handleWorkspaceConfiguration handles workspace/configuration requests from the language server
func (h *ClientHandler) handleWorkspaceConfiguration(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	// Parse the configuration params
	var params ConfigurationParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		logger.Error(fmt.Sprintf("Failed to parse configuration params: %v", err))
		if replyErr := conn.Reply(ctx, req.ID, []any{}); replyErr != nil {
			logger.Error(fmt.Sprintf("Failed to reply to configuration: %v", replyErr))
		}
		return
	}

	logger.Debug(fmt.Sprintf("Configuration requested for %d items: %+v", len(params.Items), params.Items))

	// Build configuration responses for each requested item
	responses := make([]any, len(params.Items))
	for i, item := range params.Items {
		responses[i] = h.buildConfigurationResponse(item)
	}

	// Reply with configuration array
	if err := conn.Reply(ctx, req.ID, responses); err != nil {
		logger.Error(fmt.Sprintf("Failed to reply to configuration: %v", err))
	} else {
		logger.Debug(fmt.Sprintf("Successfully replied to configuration request with %d items", len(responses)))
	}
}

// buildConfigurationResponse builds a configuration response for a specific section
func (h *ClientHandler) buildConfigurationResponse(item ConfigurationItem) any {
	logger.Debug(fmt.Sprintf("Building configuration for section: '%s', scopeUri: '%s'", item.Section, item.ScopeURI))

	// Get workspace configuration
	workspaceConfig := h.client.GetWorkspaceConfiguration()
	logger.Debug(fmt.Sprintf("WorkspaceConfiguration available: %v", workspaceConfig != nil))

	// Create template context for variable substitution
	var templateCtx *utils.TemplateContext
	workspaceFolders := h.client.ProjectRoots()
	if len(workspaceFolders) > 0 {
		templateCtx = utils.NewTemplateContext(workspaceFolders[0])
		logger.Debug(fmt.Sprintf("Template context created for workspace: %s", workspaceFolders[0]))
	}

	// If no section specified, check workspace config first
	if item.Section == "" {
		// Check if we have workspace configuration
		if workspaceConfig != nil && len(workspaceConfig) > 0 {
			logger.Debug("No section specified, returning all workspace configuration")
			if templateCtx != nil {
				substituted := templateCtx.SubstituteInMap(workspaceConfig)
				logger.Debug(fmt.Sprintf("Applied template substitution to all workspace config"))
				return substituted
			}
			return workspaceConfig
		}

		// Fallback to initialization settings
		settings := h.client.InitializationSettings()
		if len(settings) > 0 {
			logger.Debug("No section specified, returning all initialization settings")
			return settings
		}

		// Last resort: return empty config
		logger.Debug("No section and no config, returning empty object")
		return map[string]any{}
	}

	// Try to find section in workspace configuration first
	if workspaceConfig != nil {
		if sectionConfig, exists := workspaceConfig[item.Section]; exists {
			logger.Debug(fmt.Sprintf("Found workspace configuration for section '%s': %+v", item.Section, sectionConfig))

			// Apply template variable substitution
			if templateCtx != nil {
				substituted := templateCtx.SubstituteInJSON(sectionConfig)
				logger.Debug(fmt.Sprintf("Applied template substitution for section '%s'", item.Section))
				return substituted
			}
			return sectionConfig
		}
	}

	// Fallback to initialization settings
	settings := h.client.InitializationSettings()
	if sectionConfig, exists := settings[item.Section]; exists {
		logger.Debug(fmt.Sprintf("Found initialization settings for section '%s': %+v", item.Section, sectionConfig))
		return sectionConfig
	}

	// Build default configuration based on common section names
	logger.Debug(fmt.Sprintf("No config found for '%s', building default", item.Section))
	config := h.buildDefaultConfiguration(item)

	logger.Debug(fmt.Sprintf("Using default configuration for section '%s': %+v", item.Section, config))
	return config
}

// buildDefaultConfiguration builds default configuration for known language servers
func (h *ClientHandler) buildDefaultConfiguration(item ConfigurationItem) map[string]any {
	// For ESLint, provide complete required configuration
	if item.Section == "eslint" {
		config := map[string]any{
			"enable":         true,
			"packageManager": "npm",    // REQUIRED - fixes moduleResolveWorkingDirectory undefined
			"validate":       "on",     // REQUIRED - enables validation
			"useFlatConfig":  true,     // Support flat config (eslint.config.js/ts)
			"experimental": map[string]any{
				"useFlatConfig": true,  // Experimental flag for ESLint < 8.57.0
			},
			"nodePath": nil,            // Explicit nil for default behavior
			"options":  map[string]any{},
		}

		// Add working directory if we have workspace folders
		workspaceFolders := h.client.ProjectRoots()
		logger.Debug(fmt.Sprintf("ESLint config: workspaceFolders = %+v, len = %d", workspaceFolders, len(workspaceFolders)))
		if len(workspaceFolders) > 0 {
			logger.Debug(fmt.Sprintf("ESLint config: Adding workspaceFolder and workingDirectory with directory = '%s'", workspaceFolders[0]))
			// Add workspaceFolder for path resolution
			config["workspaceFolder"] = map[string]any{
				"uri":  "file://" + workspaceFolders[0],
				"name": "workspace",
			}
			// Add workingDirectory for ESLint execution
			config["workingDirectory"] = map[string]any{
				"directory": workspaceFolders[0],
			}
		}

		logger.Debug(fmt.Sprintf("ESLint config: Final config = %+v", config))
		return config
	}

	// For TypeScript, provide default settings
	if item.Section == "typescript" || item.Section == "javascript" {
		return map[string]any{
			"suggest": map[string]any{
				"enabled": true,
			},
		}
	}

	// For Tailwind CSS language server
	if item.Section == "tailwindCSS" {
		return map[string]any{
			"experimental": map[string]any{
				"configFile": nil,
			},
		}
	}

	// For editor configuration (requested by Tailwind and other servers)
	if item.Section == "editor" {
		return map[string]any{
			"tabSize":               4,
			"insertSpaces":          true,
			"detectIndentation":     true,
			"trimAutoWhitespace":    true,
			"formatOnSave":          false,
			"formatOnSaveMode":      "file",
			"quickSuggestions":      true,
			"quickSuggestionsDelay": 10,
		}
	}

	// For Python/Pyright
	if item.Section == "python" {
		return map[string]any{
			"analysis": map[string]any{
				"typeCheckingMode": "basic",
			},
		}
	}

	// Default: return empty object for unknown sections
	return map[string]any{}
}
