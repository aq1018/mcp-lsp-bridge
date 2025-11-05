package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sourcegraph/jsonrpc2"
	"rockerboo/mcp-lsp-bridge/logger"
)

// ClientHandler handles incoming messages from the language server
type ClientHandler struct {
	// Configuration context for responding to workspace/configuration requests
	client ClientContextProvider
}

// ClientContextProvider provides context information for the handler
type ClientContextProvider interface {
	ProjectRoots() []string
	InitializationSettings() map[string]interface{}
}

func (h *ClientHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	switch req.Method {
	case "textDocument/publishDiagnostics":
		// Handle diagnostics
		var params any
		if err := json.Unmarshal(*req.Params, &params); err == nil {
			logger.Debug(fmt.Sprintf("Diagnostics: %+v\n", params))
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

	// Get settings from client
	settings := h.client.InitializationSettings()
	logger.Debug(fmt.Sprintf("InitializationSettings: %+v", settings))

	// If no section specified, check if we have settings to return
	if item.Section == "" {
		// If we have custom settings, return them
		if len(settings) > 0 {
			logger.Debug("No section specified, returning all settings")
			return settings
		}

		// Otherwise, provide default ESLint configuration for empty section requests
		// vscode-eslint-language-server requests section:"" and expects full config
		logger.Debug("No section and no custom settings, building default ESLint config")
		eslintConfig := map[string]any{
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

		workspaceFolders := h.client.ProjectRoots()
		if len(workspaceFolders) > 0 {
			// Add workspaceFolder for path resolution
			eslintConfig["workspaceFolder"] = map[string]any{
				"uri":  "file://" + workspaceFolders[0],
				"name": "workspace",
			}
			// Add workingDirectory for ESLint execution
			eslintConfig["workingDirectory"] = map[string]any{
				"directory": workspaceFolders[0],
			}
			logger.Debug(fmt.Sprintf("Added workspaceFolder and workingDirectory: %s", workspaceFolders[0]))
		}

		logger.Debug(fmt.Sprintf("Returning ESLint config: %+v", eslintConfig))
		return eslintConfig
	}

	// If section exists in settings, return it
	if sectionConfig, exists := settings[item.Section]; exists {
		logger.Debug(fmt.Sprintf("Found custom configuration for section '%s': %+v", item.Section, sectionConfig))
		return sectionConfig
	}

	// Build default configuration based on common section names
	logger.Debug(fmt.Sprintf("No custom config found for '%s', building default", item.Section))
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
