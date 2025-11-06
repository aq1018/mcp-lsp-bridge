package capabilities

import (
	"sync"

	"github.com/myleshyson/lsprotocol-go/protocol"
)

// CapabilityRegistry manages client capabilities that are automatically
// registered as LSP features are implemented. This ensures that client
// capabilities always match what the bridge actually supports.
type CapabilityRegistry struct {
	mu sync.RWMutex

	// Workspace capabilities
	workspaceConfiguration    bool
	workspaceFolders          bool
	workspaceApplyEdit        bool
	workspaceSymbol           *protocol.WorkspaceSymbolClientCapabilities
	workspaceDiagnostic       *protocol.DiagnosticWorkspaceClientCapabilities

	// TextDocument capabilities
	synchronization      *protocol.TextDocumentSyncClientCapabilities
	publishDiagnostics   *protocol.PublishDiagnosticsClientCapabilities
	hover                *protocol.HoverClientCapabilities
	signatureHelp        *protocol.SignatureHelpClientCapabilities
	definition           *protocol.DefinitionClientCapabilities
	references           *protocol.ReferenceClientCapabilities
	implementation       *protocol.ImplementationClientCapabilities
	documentSymbol       *protocol.DocumentSymbolClientCapabilities
	codeAction           *protocol.CodeActionClientCapabilities
	formatting           *protocol.DocumentFormattingClientCapabilities
	rename               *protocol.RenameClientCapabilities
	semanticTokens       *protocol.SemanticTokensClientCapabilities
	callHierarchy        *protocol.CallHierarchyClientCapabilities
	diagnostic           *protocol.DiagnosticClientCapabilities

	// General capabilities
	positionEncodings    []protocol.PositionEncodingKind
	markdown             bool
}

// Global registry instance
var Registry = &CapabilityRegistry{
	positionEncodings: []protocol.PositionEncodingKind{
		protocol.PositionEncodingKindUTF16, // Default for LSP
		protocol.PositionEncodingKindUTF8,  // Preferred for performance
	},
	markdown: true, // Support markdown in hover, completion, etc.
}

// RegisterWorkspaceConfiguration registers support for workspace/configuration
func (r *CapabilityRegistry) RegisterWorkspaceConfiguration() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaceConfiguration = true
}

// RegisterWorkspaceFolders registers support for workspace folders
func (r *CapabilityRegistry) RegisterWorkspaceFolders() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaceFolders = true
}

// RegisterWorkspaceApplyEdit registers support for workspace/applyEdit
func (r *CapabilityRegistry) RegisterWorkspaceApplyEdit() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaceApplyEdit = true
}

// RegisterWorkspaceSymbol registers workspace symbol search capability
func (r *CapabilityRegistry) RegisterWorkspaceSymbol(cap *protocol.WorkspaceSymbolClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaceSymbol = cap
}

// RegisterWorkspaceDiagnostic registers workspace diagnostics capability
func (r *CapabilityRegistry) RegisterWorkspaceDiagnostic(cap *protocol.DiagnosticWorkspaceClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaceDiagnostic = cap
}

// RegisterSynchronization registers document synchronization capability
func (r *CapabilityRegistry) RegisterSynchronization(cap *protocol.TextDocumentSyncClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synchronization = cap
}

// RegisterPublishDiagnostics registers push diagnostics capability
func (r *CapabilityRegistry) RegisterPublishDiagnostics(cap *protocol.PublishDiagnosticsClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishDiagnostics = cap
}

// RegisterHover registers hover capability
func (r *CapabilityRegistry) RegisterHover(cap *protocol.HoverClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hover = cap
}

// RegisterSignatureHelp registers signature help capability
func (r *CapabilityRegistry) RegisterSignatureHelp(cap *protocol.SignatureHelpClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signatureHelp = cap
}

// RegisterDefinition registers go-to-definition capability
func (r *CapabilityRegistry) RegisterDefinition(cap *protocol.DefinitionClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.definition = cap
}

// RegisterReferences registers find-references capability
func (r *CapabilityRegistry) RegisterReferences(cap *protocol.ReferenceClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.references = cap
}

// RegisterImplementation registers find-implementation capability
func (r *CapabilityRegistry) RegisterImplementation(cap *protocol.ImplementationClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.implementation = cap
}

// RegisterDocumentSymbol registers document symbol capability
func (r *CapabilityRegistry) RegisterDocumentSymbol(cap *protocol.DocumentSymbolClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.documentSymbol = cap
}

// RegisterCodeAction registers code action capability
func (r *CapabilityRegistry) RegisterCodeAction(cap *protocol.CodeActionClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codeAction = cap
}

// RegisterFormatting registers document formatting capability
func (r *CapabilityRegistry) RegisterFormatting(cap *protocol.DocumentFormattingClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.formatting = cap
}

// RegisterRename registers rename capability
func (r *CapabilityRegistry) RegisterRename(cap *protocol.RenameClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rename = cap
}

// RegisterSemanticTokens registers semantic tokens capability
func (r *CapabilityRegistry) RegisterSemanticTokens(cap *protocol.SemanticTokensClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.semanticTokens = cap
}

// RegisterCallHierarchy registers call hierarchy capability
func (r *CapabilityRegistry) RegisterCallHierarchy(cap *protocol.CallHierarchyClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callHierarchy = cap
}

// RegisterDiagnostic registers pull diagnostics capability
func (r *CapabilityRegistry) RegisterDiagnostic(cap *protocol.DiagnosticClientCapabilities) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.diagnostic = cap
}

// Build constructs the final ClientCapabilities from all registered features
func (r *CapabilityRegistry) Build() protocol.ClientCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()

	caps := protocol.ClientCapabilities{
		General: &protocol.GeneralClientCapabilities{
			PositionEncodings: r.positionEncodings,
		},
	}

	// Workspace capabilities
	if r.workspaceConfiguration || r.workspaceFolders || r.workspaceApplyEdit ||
		r.workspaceSymbol != nil || r.workspaceDiagnostic != nil {

		caps.Workspace = &protocol.WorkspaceClientCapabilities{
			Configuration:    r.workspaceConfiguration,
			WorkspaceFolders: r.workspaceFolders,
			ApplyEdit:        r.workspaceApplyEdit,
			Symbol:           r.workspaceSymbol,
			Diagnostics:      r.workspaceDiagnostic,
		}
	}

	// TextDocument capabilities
	caps.TextDocument = &protocol.TextDocumentClientCapabilities{
		Synchronization:     r.synchronization,
		PublishDiagnostics:  r.publishDiagnostics,
		Hover:               r.hover,
		SignatureHelp:       r.signatureHelp,
		Definition:          r.definition,
		References:          r.references,
		Implementation:      r.implementation,
		DocumentSymbol:      r.documentSymbol,
		CodeAction:          r.codeAction,
		Formatting:          r.formatting,
		Rename:              r.rename,
		SemanticTokens:      r.semanticTokens,
		CallHierarchy:       r.callHierarchy,
		Diagnostic:          r.diagnostic,
	}

	return caps
}

// ListRegistered returns a human-readable list of registered capabilities
// Useful for debugging and validation
func (r *CapabilityRegistry) ListRegistered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var registered []string

	// Workspace
	if r.workspaceConfiguration {
		registered = append(registered, "workspace.configuration")
	}
	if r.workspaceFolders {
		registered = append(registered, "workspace.workspaceFolders")
	}
	if r.workspaceApplyEdit {
		registered = append(registered, "workspace.applyEdit")
	}
	if r.workspaceSymbol != nil {
		registered = append(registered, "workspace.symbol")
	}
	if r.workspaceDiagnostic != nil {
		registered = append(registered, "workspace.diagnostic")
	}

	// TextDocument
	if r.synchronization != nil {
		registered = append(registered, "textDocument.synchronization")
	}
	if r.publishDiagnostics != nil {
		registered = append(registered, "textDocument.publishDiagnostics")
	}
	if r.hover != nil {
		registered = append(registered, "textDocument.hover")
	}
	if r.signatureHelp != nil {
		registered = append(registered, "textDocument.signatureHelp")
	}
	if r.definition != nil {
		registered = append(registered, "textDocument.definition")
	}
	if r.references != nil {
		registered = append(registered, "textDocument.references")
	}
	if r.implementation != nil {
		registered = append(registered, "textDocument.implementation")
	}
	if r.documentSymbol != nil {
		registered = append(registered, "textDocument.documentSymbol")
	}
	if r.codeAction != nil {
		registered = append(registered, "textDocument.codeAction")
	}
	if r.formatting != nil {
		registered = append(registered, "textDocument.formatting")
	}
	if r.rename != nil {
		registered = append(registered, "textDocument.rename")
	}
	if r.semanticTokens != nil {
		registered = append(registered, "textDocument.semanticTokens")
	}
	if r.callHierarchy != nil {
		registered = append(registered, "textDocument.callHierarchy")
	}
	if r.diagnostic != nil {
		registered = append(registered, "textDocument.diagnostic")
	}

	return registered
}
