package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/utils"
)

// RegisterDocumentCloseTool registers the document_close tool
func RegisterDocumentCloseTool(server ToolServer, bridge interfaces.BridgeInterface) {
	tool := mcp.NewTool("document_close",
		mcp.WithDescription("Sends a textDocument/didClose notification to the language server. This tells the language server that the document is no longer being edited and frees up resources."),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("The URI of the document to close (e.g., file:///path/to/file.ts)")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("document_close: Processing request")

		// Extract and validate parameters
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError("uri parameter is required and must be a string"), nil
		}

		// Normalize URI
		normalizedURI := utils.NormalizeURI(uri)
		logger.Debug(fmt.Sprintf("document_close: URI: %s | Normalized: %s", uri, normalizedURI))

		// Infer language from URI
		language, err := bridge.InferLanguage(normalizedURI)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to determine language for URI %s: %v", normalizedURI, err)), nil
		}
		logger.Debug(fmt.Sprintf("document_close: Detected language: %s", string(*language)))

		// Get ALL clients for the language
		clients, serverNames, err := bridge.GetAllClientsForLanguage(string(*language))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get clients for language %s: %v", string(*language), err)), nil
		}

		logger.Info(fmt.Sprintf("document_close: Sending didClose to %d language server(s): %v", len(clients), serverNames))

		// Send didClose to each language server
		for i, client := range clients {
			serverName := serverNames[i]

			err := client.DidClose(normalizedURI)
			if err != nil {
				logger.Warn(fmt.Sprintf("document_close: Failed to send didClose to %s: %v", serverName, err))
			} else {
				logger.Info(fmt.Sprintf("document_close: ✓ Sent didClose to %s", serverName))
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("Sent didClose notification to %d language server(s) for %s", len(clients), normalizedURI)), nil
	}

	server.AddTool(tool, handler)
	logger.Debug("Registered document_close tool")
}
