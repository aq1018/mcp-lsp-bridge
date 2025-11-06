package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yosida95/uritemplate/v3"
	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
)

// DiagnosticsResourceHandler handles diagnostics resources
type DiagnosticsResourceHandler struct {
	bridge    interfaces.BridgeInterface
	mcpServer *server.MCPServer
}

// NewDiagnosticsResourceHandler creates a new diagnostics resource handler
func NewDiagnosticsResourceHandler(bridge interfaces.BridgeInterface, mcpServer *server.MCPServer) *DiagnosticsResourceHandler {
	return &DiagnosticsResourceHandler{
		bridge:    bridge,
		mcpServer: mcpServer,
	}
}

// RegisterResourceHandlers registers all resource-related handlers
func RegisterResourceHandlers(mcpServer *server.MCPServer, bridge interfaces.BridgeInterface) *DiagnosticsResourceHandler {
	handler := NewDiagnosticsResourceHandler(bridge, mcpServer)

	// Create a resource template for diagnostics
	// This allows clients to request diagnostics for any file URI using the pattern: diagnostics://file:///path/to/file
	uriTemplate, err := uritemplate.New("diagnostics://{uri}")
	if err != nil {
		logger.Error("Failed to create URI template:", err)
		return nil
	}

	template := mcp.ResourceTemplate{
		URITemplate: &mcp.URITemplate{Template: uriTemplate},
		Name:        "File Diagnostics",
		Description: "Get LSP diagnostics for a specific file. Use the pattern diagnostics://file:///path/to/file",
		MIMEType:    "application/json",
	}

	// Register the resource template handler
	mcpServer.AddResourceTemplate(template, handler.handleDiagnosticsRead)

	logger.Info("Registered diagnostics resource template")

	return handler
}

// handleDiagnosticsRead handles requests to read diagnostics resources
func (h *DiagnosticsResourceHandler) handleDiagnosticsRead(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	logger.Debug(fmt.Sprintf("diagnostics resource read called for URI: %s", request.Params.URI))

	// Extract file URI from resource URI
	// Format: diagnostics://file:///path/to/file.ts
	if len(request.Params.URI) < 15 || !strings.HasPrefix(request.Params.URI, "diagnostics://") {
		return nil, fmt.Errorf("invalid diagnostics resource URI: %s", request.Params.URI)
	}

	fileURI := strings.TrimPrefix(request.Params.URI, "diagnostics://")
	logger.Debug(fmt.Sprintf("Extracted file URI: %s", fileURI))

	// Get diagnostics from bridge
	report, err := h.bridge.GetDocumentDiagnostics(fileURI, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to get diagnostics for %s: %w", fileURI, err)
	}

	// Convert report to JSON
	diagnosticsJSON, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal diagnostics: %w", err)
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      request.Params.URI,
			MIMEType: "application/json",
			Text:     string(diagnosticsJSON),
		},
	}, nil
}

// NotifyDiagnosticsUpdated sends a notification to all clients when diagnostics are updated
// Note: This requires clients to be subscribed to the resource, which is not yet fully implemented
// in mcp-go v0.32.0. Clients will need to poll for updates or we need to implement custom
// subscription handling.
func (h *DiagnosticsResourceHandler) NotifyDiagnosticsUpdated(fileURI string) {
	resourceURI := "diagnostics://" + fileURI
	logger.Debug(fmt.Sprintf("Diagnostics updated for: %s", resourceURI))

	// Send notification to all clients
	// Note: This uses the notifications/resources/updated method from MCP spec
	h.mcpServer.SendNotificationToAllClients("notifications/resources/updated", map[string]any{
		"uri": resourceURI,
	})
}
