package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/utils"
)

// RegisterDocumentSaveTool registers the document_save tool
func RegisterDocumentSaveTool(server ToolServer, bridge interfaces.BridgeInterface) {
	tool := mcp.NewTool("document_save",
		mcp.WithDescription("Sends a textDocument/didSave notification to the language server. This triggers the language server to perform a full analysis of the document and may result in updated diagnostics."),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("The URI of the document to save (e.g., file:///path/to/file.ts)")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("document_save: Processing request")

		// Extract and validate parameters
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError("uri parameter is required and must be a string"), nil
		}

		// Normalize URI
		normalizedURI := utils.NormalizeURI(uri)
		logger.Debug(fmt.Sprintf("document_save: URI: %s | Normalized: %s", uri, normalizedURI))

		// Infer language from URI
		language, err := bridge.InferLanguage(normalizedURI)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to determine language for URI %s: %v", normalizedURI, err)), nil
		}
		logger.Debug(fmt.Sprintf("document_save: Detected language: %s", string(*language)))

		// Get ALL clients for the language
		clients, serverNames, err := bridge.GetAllClientsForLanguage(string(*language))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get clients for language %s: %v", string(*language), err)), nil
		}

		logger.Info(fmt.Sprintf("document_save: Sending didSave to %d language server(s): %v", len(clients), serverNames))

		// Send didSave to each language server
		for i, client := range clients {
			serverName := serverNames[i]

			err := client.DidSave(normalizedURI, nil)
			if err != nil {
				logger.Warn(fmt.Sprintf("document_save: Failed to send didSave to %s: %v", serverName, err))
			} else {
				logger.Info(fmt.Sprintf("document_save: ✓ Sent didSave to %s", serverName))
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("Sent didSave notification to %d language server(s) for %s", len(clients), normalizedURI)), nil
	}

	server.AddTool(tool, handler)
	logger.Debug("Registered document_save tool")
}
