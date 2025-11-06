package resources

import (
	"encoding/json"
	"fmt"
	"strings"

	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
)

// init auto-registers the diagnostics resource when the package is imported
// This follows the same pattern as lsp/capabilities/features.go
func init() {
	Registry.Register(&ResourceDefinition{
		Name:        "diagnostics",
		UriTemplate: "diagnostics://{uri}",
		Description: "LSP diagnostics for a specific file. Use the pattern diagnostics://file:///path/to/file",
		MimeType:    "application/json",
		ReadHandler: handleDiagnosticsRead,
		OnSetup:     setupDiagnosticsNotifications,
	})
}

// handleDiagnosticsRead handles requests to read diagnostics resources
// Called when a client requests diagnostics://file:///path/to/file.ts
func handleDiagnosticsRead(uri string, bridge interfaces.BridgeInterface) (interface{}, error) {
	logger.Debug(fmt.Sprintf("Diagnostics resource read for URI: %s", uri))

	// Extract file URI from resource URI
	// Format: diagnostics://file:///path/to/file.ts
	if len(uri) < 15 || !strings.HasPrefix(uri, "diagnostics://") {
		return nil, fmt.Errorf("invalid diagnostics resource URI: %s", uri)
	}

	fileURI := strings.TrimPrefix(uri, "diagnostics://")
	logger.Debug(fmt.Sprintf("Extracted file URI: %s", fileURI))

	// Get diagnostics from bridge
	report, err := bridge.GetDocumentDiagnostics(fileURI, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to get diagnostics for %s: %w", fileURI, err)
	}

	// Convert report to JSON string for the resource content
	diagnosticsJSON, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal diagnostics: %w", err)
	}

	// Return as string (MCP resources expect text content)
	return string(diagnosticsJSON), nil
}

// setupDiagnosticsNotifications wires up the diagnostics callback to send notifications
// Called once during initialization to enable push notifications when diagnostics change
func setupDiagnosticsNotifications(bridge interfaces.BridgeInterface) error {
	logger.Debug("Setting up diagnostics push notifications")

	// Hook into the bridge's diagnostics callback system
	// When diagnostics change for a file, notify all subscribers
	bridge.SetDiagnosticsCallback(func(fileURI string) {
		resourceURI := "diagnostics://" + fileURI
		logger.Debug(fmt.Sprintf("Diagnostics changed, notifying subscribers: %s", resourceURI))

		// Notify all subscribers of this resource
		Registry.Notify(resourceURI, nil)
	})

	logger.Info("Diagnostics push notifications enabled")
	return nil
}
