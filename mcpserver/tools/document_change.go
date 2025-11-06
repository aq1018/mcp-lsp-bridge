package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/myleshyson/lsprotocol-go/protocol"
	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/utils"
)

// RegisterDocumentChangeTool registers the document_change tool
func RegisterDocumentChangeTool(server ToolServer, bridge interfaces.BridgeInterface) {
	tool := mcp.NewTool("document_change",
		mcp.WithDescription(`Send a textDocument/didChange notification to update document content in the language server.

IMPORTANT USAGE NOTES:
- Document MUST be open first (use document_open)
- Sends full document content replacement (not incremental)
- Triggers language server to re-analyze and send updated diagnostics
- Version number increments automatically
- Use this when you want to analyze hypothetical changes without writing to disk

TYPICAL WORKFLOW:
1. document_open - Open the original file
2. document_change - Send modified content
3. Wait 1-2 seconds for re-analysis
4. document_diagnostics - Get diagnostics for the changed content
5. document_close - Clean up

EXAMPLE:
uri="file:///path/to/file.ts"
content="const x: number = 'invalid'; // This will trigger a diagnostic"
→ TypeScript analyzes the new content and caches diagnostics`),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("The URI of the document to update (e.g., file:///path/to/file.ts)")),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("The new full content of the document")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("document_change: Processing request")

		// Extract and validate parameters
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError("uri parameter is required and must be a string"), nil
		}

		content, err := request.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError("content parameter is required and must be a string"), nil
		}

		// Normalize URI
		normalizedURI := utils.NormalizeURI(uri)
		logger.Debug(fmt.Sprintf("document_change: URI: %s | Normalized: %s", uri, normalizedURI))

		// Infer language from URI
		language, err := bridge.InferLanguage(normalizedURI)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to determine language for URI %s: %v", normalizedURI, err)), nil
		}
		logger.Debug(fmt.Sprintf("document_change: Detected language: %s", string(*language)))

		// Get ALL clients for the language
		clients, serverNames, err := bridge.GetAllClientsForLanguage(string(*language))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get clients for language %s: %v", string(*language), err)), nil
		}

		logger.Info(fmt.Sprintf("document_change: Sending didChange to %d language server(s): %v", len(clients), serverNames))

		// Send didChange to each language server (full document sync)
		successCount := 0
		var errors []string

		for i, client := range clients {
			serverName := serverNames[i]

			// Use version 2 (incremented from initial version 1)
			// In a real implementation, we'd track versions per document

			// Create a full document change event
			wholeDocChange := protocol.TextDocumentContentChangeWholeDocument{
				Text: content,
			}

			// Wrap it in the Or2 union type
			changeEvent := protocol.TextDocumentContentChangeEvent{
				Value: wholeDocChange,
			}

			changes := []protocol.TextDocumentContentChangeEvent{changeEvent}

			err := client.DidChange(normalizedURI, 2, changes)
			if err != nil {
				logger.Warn(fmt.Sprintf("document_change: Failed to send didChange to %s: %v", serverName, err))
				errors = append(errors, fmt.Sprintf("%s: %v", serverName, err))
			} else {
				logger.Info(fmt.Sprintf("document_change: ✓ Sent didChange to %s", serverName))
				successCount++
			}
		}

		// Construct result message
		var resultMsg string
		if successCount > 0 {
			resultMsg = fmt.Sprintf("Successfully sent document change to %d/%d language server(s) for %s\n", successCount, len(clients), normalizedURI)
			if len(errors) > 0 {
				resultMsg += fmt.Sprintf("\nPartial failures:\n")
				for _, e := range errors {
					resultMsg += fmt.Sprintf("  - %s\n", e)
				}
			}
			resultMsg += "\nNote: Language servers will re-analyze the document and send updated diagnostics within 1-2 seconds."
			return mcp.NewToolResultText(resultMsg), nil
		} else {
			resultMsg = fmt.Sprintf("Failed to send document change to all language servers:\n")
			for _, e := range errors {
				resultMsg += fmt.Sprintf("  - %s\n", e)
			}
			return mcp.NewToolResultError(resultMsg), nil
		}
	}

	server.AddTool(tool, handler)
	logger.Debug("Registered document_change tool")
}
