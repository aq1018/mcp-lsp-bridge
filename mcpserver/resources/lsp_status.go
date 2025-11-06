package resources

import (
	"encoding/json"
	"fmt"
	"strings"

	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
)

// init auto-registers the LSP server status resource
func init() {
	Registry.Register(&ResourceDefinition{
		Name:        "lsp-status",
		UriTemplate: "lsp-server://{server_name}/status",
		Description: "Connection status and metrics for a language server. Use the pattern lsp-server://gopls/status",
		MimeType:    "application/json",
		ReadHandler: handleLspStatusRead,
		OnSetup:     setupLspStatusNotifications,
	})
}

// LSPServerStatus represents the status of a language server
type LSPServerStatus struct {
	ServerName     string `json:"serverName"`
	Status         string `json:"status"` // "connected", "disconnected", "connecting", "error"
	IsConnected    bool   `json:"isConnected"`
	TotalRequests  int64  `json:"totalRequests"`
	FailedRequests int64  `json:"failedRequests"`
	LastError      string `json:"lastError,omitempty"`
}

// handleLspStatusRead handles requests to read LSP server status
// Called when a client requests lsp-server://gopls/status
func handleLspStatusRead(uri string, bridge interfaces.BridgeInterface) (interface{}, error) {
	logger.Debug(fmt.Sprintf("LSP status resource read for URI: %s", uri))

	// Extract server name from resource URI
	// Format: lsp-server://gopls/status
	if !strings.HasPrefix(uri, "lsp-server://") {
		return nil, fmt.Errorf("invalid LSP status resource URI: %s", uri)
	}

	// Parse: lsp-server://server-name/status
	parts := strings.TrimPrefix(uri, "lsp-server://")
	parts = strings.TrimSuffix(parts, "/status")
	serverName := parts

	if serverName == "" {
		return nil, fmt.Errorf("no server name specified in URI: %s", uri)
	}

	logger.Debug(fmt.Sprintf("Extracted server name: %s", serverName))

	// Get client for this language server
	// Note: We need to map server name to language first
	// For now, return a placeholder indicating this needs implementation
	status := LSPServerStatus{
		ServerName:  serverName,
		Status:      "unknown",
		IsConnected: false,
	}

	// TODO: Get actual status from bridge
	// This would require:
	// 1. Bridge method to get client by server name (not just by language)
	// 2. Client metrics/status accessor

	// Convert to JSON string
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal status: %w", err)
	}

	return string(statusJSON), nil
}

// setupLspStatusNotifications wires up LSP status change notifications
// Called once during initialization
func setupLspStatusNotifications(bridge interfaces.BridgeInterface) error {
	logger.Debug("Setting up LSP status push notifications")

	// TODO: Hook into client lifecycle events
	// This would require:
	// 1. Bridge exposing client lifecycle hooks
	// 2. Callback when client connects/disconnects/errors
	//
	// Example future implementation:
	// bridge.OnClientStatusChange(func(serverName, status string) {
	//     resourceURI := fmt.Sprintf("lsp-server://%s/status", serverName)
	//     Registry.Notify(resourceURI, map[string]interface{}{
	//         "serverName": serverName,
	//         "status": status,
	//     })
	// })

	logger.Info("LSP status push notifications setup (pending lifecycle hooks)")
	return nil
}
