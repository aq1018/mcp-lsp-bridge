package capabilities

import (
	"github.com/myleshyson/lsprotocol-go/protocol"
)

// This file registers all LSP features that mcp-lsp-bridge currently implements.
// When you add a new LSP feature, add its capability registration here.
//
// IMPORTANT: Only register capabilities for features that are actually implemented!
// Language servers check these capabilities and may send requests/notifications
// based on what you advertise.

func init() {
	// ====================
	// WORKSPACE CAPABILITIES
	// ====================

	// workspace/configuration - Required by Tailwind CSS, ESLint, and other servers
	// that need dynamic configuration
	Registry.RegisterWorkspaceConfiguration()

	// workspace/workspaceFolders - Multi-root workspace support
	// Required by some language servers (Tailwind CSS)
	Registry.RegisterWorkspaceFolders()

	// workspace/symbol - Project-wide symbol search
	// Implemented in: bridge.SearchTextInWorkspace(), bridge.SearchTextInAllLanguages()
	// Exposed via: symbol_explore MCP tool
	Registry.RegisterWorkspaceSymbol(&protocol.WorkspaceSymbolClientCapabilities{
		DynamicRegistration: false,
	})

	// workspace/diagnostic - Pull diagnostics for entire workspace
	// Implemented in: bridge.GetWorkspaceDiagnostics()
	// Exposed via: workspace_diagnostics MCP tool
	Registry.RegisterWorkspaceDiagnostic(&protocol.DiagnosticWorkspaceClientCapabilities{
		RefreshSupport: false,
	})

	// ====================
	// DOCUMENT LIFECYCLE
	// ====================

	// textDocument/did* - Document synchronization
	// Implemented in: lsp.DidOpen(), lsp.DidChange(), lsp.DidSave(), lsp.DidClose()
	// Exposed via: document_open, document_change, document_save, document_close MCP tools
	Registry.RegisterSynchronization(&protocol.TextDocumentSyncClientCapabilities{
		DynamicRegistration: false,
		WillSave:            false, // Not implemented
		WillSaveWaitUntil:   false, // Not implemented
		DidSave:             true,  // ✅ Implemented
	})

	// ====================
	// DIAGNOSTICS
	// ====================

	// textDocument/publishDiagnostics - Push diagnostics from server
	// **THIS IS CRITICAL FOR TYPESCRIPT AND OTHER LANGUAGE SERVERS**
	// Implemented in: lsp.Handler.handlePublishDiagnostics() - caches diagnostics
	// Retrieved via: lsp.GetCachedDiagnostics()
	// Exposed via: document_diagnostics MCP tool
	Registry.RegisterPublishDiagnostics(&protocol.PublishDiagnosticsClientCapabilities{
		RelatedInformation:     true, // Show related diagnostic info
		TagSupport:             &protocol.ClientDiagnosticsTagOptions{ValueSet: []protocol.DiagnosticTag{1, 2}}, // Unnecessary(1) and Deprecated(2)
		VersionSupport:         false, // Don't need versioned diagnostics
		CodeDescriptionSupport: true,  // Show documentation links for error codes
		DataSupport:            true,  // Support arbitrary diagnostic data
	})

	// textDocument/diagnostic - Pull diagnostics (LSP 3.17+)
	// Implemented in: lsp.DocumentDiagnostics()
	// Exposed via: document_diagnostics MCP tool
	Registry.RegisterDiagnostic(&protocol.DiagnosticClientCapabilities{
		DynamicRegistration:    false,
		RelatedDocumentSupport: false, // Don't need related document diagnostics
	})

	// ====================
	// CODE INTELLIGENCE
	// ====================

	// textDocument/hover - Hover information
	// Implemented in: lsp.Hover(), bridge.GetHoverInformation()
	// Exposed via: hover MCP tool
	Registry.RegisterHover(&protocol.HoverClientCapabilities{
		DynamicRegistration: false,
		ContentFormat:       []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
	})

	// textDocument/signatureHelp - Function signature help
	// Implemented in: lsp.SignatureHelp(), bridge.GetSignatureHelp()
	// Exposed via: signature_help MCP tool
	Registry.RegisterSignatureHelp(&protocol.SignatureHelpClientCapabilities{
		DynamicRegistration: false,
	})

	// textDocument/definition - Go to definition
	// Implemented in: lsp.Definition(), bridge.FindSymbolDefinitions()
	// Exposed via: symbol_explore MCP tool
	Registry.RegisterDefinition(&protocol.DefinitionClientCapabilities{
		DynamicRegistration: false,
		LinkSupport:         true, // Support LocationLink for more precise navigation
	})

	// textDocument/references - Find all references
	// Implemented in: lsp.References(), bridge.FindSymbolReferences()
	// Exposed via: symbol_explore MCP tool
	Registry.RegisterReferences(&protocol.ReferenceClientCapabilities{
		DynamicRegistration: false,
	})

	// textDocument/implementation - Find implementations
	// Implemented in: lsp.Implementation(), bridge.FindImplementations()
	// Exposed via: implementation MCP tool
	Registry.RegisterImplementation(&protocol.ImplementationClientCapabilities{
		DynamicRegistration: false,
		LinkSupport:         true,
	})

	// textDocument/documentSymbol - Document outline/symbols
	// Implemented in: lsp.DocumentSymbols(), bridge.GetDocumentSymbols()
	// Exposed via: symbol_explore MCP tool
	Registry.RegisterDocumentSymbol(&protocol.DocumentSymbolClientCapabilities{
		DynamicRegistration:               false,
		HierarchicalDocumentSymbolSupport: true, // Support hierarchical symbols (classes, methods, etc.)
	})

	// ====================
	// CODE ACTIONS & REFACTORING
	// ====================

	// textDocument/codeAction - Quick fixes and refactorings
	// Implemented in: lsp.CodeActions(), bridge.GetCodeActions()
	// Exposed via: code_actions MCP tool
	Registry.RegisterCodeAction(&protocol.CodeActionClientCapabilities{
		DynamicRegistration: false,
		DataSupport:          true,
		IsPreferredSupport:   true,
		DisabledSupport:      true,
	})

	// textDocument/formatting - Document formatting
	// Implemented in: lsp.Formatting(), bridge.FormatDocument()
	// Exposed via: format_document MCP tool
	Registry.RegisterFormatting(&protocol.DocumentFormattingClientCapabilities{
		DynamicRegistration: false,
	})

	// textDocument/rename - Symbol renaming
	// Implemented in: lsp.Rename(), bridge.RenameSymbol()
	// Exposed via: rename_symbol MCP tool
	Registry.RegisterRename(&protocol.RenameClientCapabilities{
		DynamicRegistration: false,
		PrepareSupport:      false, // prepareRename not implemented yet
	})

	// ====================
	// ADVANCED FEATURES
	// ====================

	// textDocument/semanticTokens - Semantic highlighting
	// Implemented in: lsp.SemanticTokens(), lsp.SemanticTokensRange()
	// Exposed via: semantic_tokens MCP tool
	Registry.RegisterSemanticTokens(&protocol.SemanticTokensClientCapabilities{
		DynamicRegistration: false,
		Formats:             []protocol.TokenFormat{protocol.TokenFormatRelative},
		Requests: protocol.ClientSemanticTokensRequestOptions{
			Full:  &protocol.Or2[bool, protocol.ClientSemanticTokensRequestFullDelta]{Value: true},
			Range: &protocol.Or2[bool, protocol.LSPObject]{Value: true},
		},
		TokenModifiers: []string{}, // Must be empty array, not nil
		TokenTypes:     []string{}, // Must be empty array, not nil
	})

	// callHierarchy/* - Call hierarchy navigation
	// Implemented in: lsp.PrepareCallHierarchy(), lsp.IncomingCalls(), lsp.OutgoingCalls()
	// Exposed via: call_hierarchy MCP tool
	Registry.RegisterCallHierarchy(&protocol.CallHierarchyClientCapabilities{
		DynamicRegistration: false,
	})
}
