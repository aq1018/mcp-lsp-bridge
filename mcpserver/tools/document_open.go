package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/myleshyson/lsprotocol-go/protocol"
	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
	"rockerboo/mcp-lsp-bridge/utils"
)

// RegisterDocumentOpenTool registers the document_open tool
func RegisterDocumentOpenTool(server ToolServer, bridge interfaces.BridgeInterface) {
	tool := mcp.NewTool("document_open",
		mcp.WithDescription(`Explicitly open a document in the language server. This sends a textDocument/didOpen notification.

IMPORTANT USAGE NOTES:
- You MUST open a document before requesting diagnostics, hover, or other LSP features
- Opening a document triggers the language server to analyze it and send diagnostics (usually within 1-2 seconds)
- Documents remain open until explicitly closed with document_close
- Attempting to open an already-open document will return an error
- This is the FIRST step in the typical workflow: open → get diagnostics → (optionally) close

TYPICAL WORKFLOW:
1. document_open - Open the file (triggers analysis)
2. Wait 1-2 seconds for language server to analyze
3. document_diagnostics - Get the diagnostics from cache
4. (Optional) document_close - Clean up when done

EXAMPLE:
uri="file:///path/to/file.ts" → Opens file, TypeScript analyzes it, diagnostics cached`),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("The URI of the document to open (e.g., file:///path/to/file.ts)")),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logger.Info("document_open: Processing request")

		// Extract and validate parameters
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError("uri parameter is required and must be a string"), nil
		}

		// Normalize URI
		normalizedURI := utils.NormalizeURI(uri)
		logger.Debug(fmt.Sprintf("document_open: URI: %s | Normalized: %s", uri, normalizedURI))

		// Infer language from URI
		language, err := bridge.InferLanguage(normalizedURI)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to determine language for URI %s: %v", normalizedURI, err)), nil
		}
		logger.Debug(fmt.Sprintf("document_open: Detected language: %s", string(*language)))

		// Get ALL clients for the language
		clients, serverNames, err := bridge.GetAllClientsForLanguage(string(*language))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get clients for language %s: %v", string(*language), err)), nil
		}

		logger.Info(fmt.Sprintf("document_open: Opening in %d language server(s): %v", len(clients), serverNames))

		// Read file content
		filePath := strings.TrimPrefix(normalizedURI, "file://")
		content, err := os.ReadFile(filePath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read file %s: %v", filePath, err)), nil
		}

		// Send didOpen to each language server
		successCount := 0
		var errors []string

		for i, client := range clients {
			serverName := serverNames[i]

			err := client.DidOpen(normalizedURI, protocol.LanguageKind(string(*language)), string(content), 1)
			if err != nil {
				if strings.Contains(err.Error(), "already open") {
					logger.Info(fmt.Sprintf("document_open: Document already open in %s", serverName))
					errors = append(errors, fmt.Sprintf("%s: already open", serverName))
				} else {
					logger.Warn(fmt.Sprintf("document_open: Failed to send didOpen to %s: %v", serverName, err))
					errors = append(errors, fmt.Sprintf("%s: %v", serverName, err))
				}
			} else {
				logger.Info(fmt.Sprintf("document_open: ✓ Sent didOpen to %s", serverName))
				successCount++
			}
		}

		// Construct result message
		var resultMsg string
		if successCount > 0 {
			resultMsg = fmt.Sprintf("Successfully opened document in %d/%d language server(s) for %s\n", successCount, len(clients), normalizedURI)
			if len(errors) > 0 {
				resultMsg += fmt.Sprintf("\nPartial failures:\n")
				for _, e := range errors {
					resultMsg += fmt.Sprintf("  - %s\n", e)
				}
			}
			resultMsg += "\nNote: Language servers will analyze the document and send diagnostics within 1-2 seconds. Use document_diagnostics to retrieve them."
			return mcp.NewToolResultText(resultMsg), nil
		} else {
			resultMsg = fmt.Sprintf("Failed to open document in all language servers:\n")
			for _, e := range errors {
				resultMsg += fmt.Sprintf("  - %s\n", e)
			}
			return mcp.NewToolResultError(resultMsg), nil
		}
	}

	server.AddTool(tool, handler)
	logger.Debug("Registered document_open tool")
}
