package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yosida95/uritemplate/v3"
	"rockerboo/mcp-lsp-bridge/interfaces"
	"rockerboo/mcp-lsp-bridge/logger"
)

// ResourceRegistry manages all registered MCP resources and their subscriptions
// Similar to lsp/capabilities/registry.go - resources auto-register via init()
type ResourceRegistry struct {
	mu            sync.RWMutex
	resources     []RegisteredResource
	subscriptions *SubscriptionTracker
	bridge        interfaces.BridgeInterface
	mcpServer     *server.MCPServer
}

// Global registry instance - resources register themselves here via init()
var Registry = &ResourceRegistry{
	subscriptions: NewSubscriptionTracker(),
	resources:     make([]RegisteredResource, 0),
}

// Register adds a resource definition to the registry
// Called from init() functions in resource files (diagnostics.go, lsp_status.go, etc.)
func (r *ResourceRegistry) Register(resource *ResourceDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate resource definition
	if resource.Name == "" {
		logger.Warn("Skipping resource registration: Name is empty")
		return
	}
	if resource.UriTemplate == "" {
		logger.Warn(fmt.Sprintf("Skipping resource %s: UriTemplate is empty", resource.Name))
		return
	}
	if resource.ReadHandler == nil {
		logger.Warn(fmt.Sprintf("Skipping resource %s: ReadHandler is nil", resource.Name))
		return
	}

	r.resources = append(r.resources, RegisteredResource{
		Definition: resource,
	})

	logger.Debug(fmt.Sprintf("Registered resource: %s (template: %s)", resource.Name, resource.UriTemplate))
}

// Build registers all resources with the MCP server and sets up handlers
// Called once during server initialization, similar to capabilities.Registry.Build()
func (r *ResourceRegistry) Build(mcpServer *server.MCPServer, bridge interfaces.BridgeInterface) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.bridge = bridge
	r.mcpServer = mcpServer

	// Register all resource templates with the MCP server
	for _, registered := range r.resources {
		res := registered.Definition

		// Create resource template using uritemplate
		uriTemplate, err := uritemplate.New(res.UriTemplate)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to create URI template for %s: %v", res.Name, err))
			return fmt.Errorf("failed to create URI template for %s: %w", res.Name, err)
		}

		resourceTemplate := mcp.ResourceTemplate{
			URITemplate: &mcp.URITemplate{Template: uriTemplate},
			Name:        res.Name,
			Description: res.Description,
			MIMEType:    res.MimeType,
		}

		// Create handler wrapper that matches ResourceTemplateHandlerFunc signature
		// ResourceTemplateHandlerFunc = func(ctx, request) ([]ResourceContents, error)
		handlerWrapper := func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			// Call the user's read handler
			result, err := res.ReadHandler(request.Params.URI, bridge)
			if err != nil {
				return nil, err
			}

			// Convert result to ResourceContents
			var contents []mcp.ResourceContents

			// If result is already a string, use it directly
			if strResult, ok := result.(string); ok {
				contents = []mcp.ResourceContents{
					mcp.TextResourceContents{
						URI:      request.Params.URI,
						MIMEType: res.MimeType,
						Text:     strResult,
					},
				}
			} else {
				// Otherwise, marshal to JSON
				jsonBytes, err := json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal result: %w", err)
				}

				contents = []mcp.ResourceContents{
					mcp.TextResourceContents{
						URI:      request.Params.URI,
						MIMEType: res.MimeType,
						Text:     string(jsonBytes),
					},
				}
			}

			return contents, nil
		}

		// Register the template with read handler
		mcpServer.AddResourceTemplate(resourceTemplate, handlerWrapper)

		// Call OnSetup hook to wire up notifications
		if res.OnSetup != nil {
			if err := res.OnSetup(bridge); err != nil {
				logger.Error(fmt.Sprintf("Failed to setup notifications for resource %s: %v", res.Name, err))
				return fmt.Errorf("failed to setup resource %s: %w", res.Name, err)
			}
		}

		logger.Info(fmt.Sprintf("Built resource: %s (template: %s)", res.Name, res.UriTemplate))
	}

	// Add subscription handlers (resources/subscribe and resources/unsubscribe)
	// Note: These are not part of the standard mcp-go library, we're adding them manually
	if err := r.addSubscriptionHandlers(mcpServer); err != nil {
		return fmt.Errorf("failed to add subscription handlers: %w", err)
	}

	logger.Info(fmt.Sprintf("Resource registry built: %d resources registered", len(r.resources)))
	return nil
}

// addSubscriptionHandlers adds custom handlers for resources/subscribe and resources/unsubscribe
func (r *ResourceRegistry) addSubscriptionHandlers(mcpServer *server.MCPServer) error {
	// Note: mcp-go v0.32.0 doesn't have built-in subscription support
	// We're adding these handlers manually to enable custom clients

	// TODO: This requires extending mcp-go to support custom request handlers
	// For now, we'll log that subscription handlers are not yet implemented
	logger.Info("Subscription handlers (resources/subscribe, resources/unsubscribe) pending mcp-go library support")

	// When mcp-go supports custom handlers, implement:
	// mcpServer.AddRequestHandler("resources/subscribe", r.handleSubscribe)
	// mcpServer.AddRequestHandler("resources/unsubscribe", r.handleUnsubscribe)

	return nil
}

// handleSubscribe handles resources/subscribe requests
// Will be used once mcp-go supports custom request handlers
func (r *ResourceRegistry) handleSubscribe(ctx context.Context, request map[string]interface{}) (interface{}, error) {
	// Extract client ID and resource URI from request
	clientID, _ := request["clientId"].(string)
	uri, _ := request["uri"].(string)

	if clientID == "" || uri == "" {
		return nil, fmt.Errorf("missing clientId or uri in subscribe request")
	}

	// Add subscription
	r.subscriptions.Subscribe(clientID, uri)

	logger.Debug(fmt.Sprintf("Client %s subscribed to resource: %s", clientID, uri))

	return map[string]interface{}{
		"success": true,
	}, nil
}

// handleUnsubscribe handles resources/unsubscribe requests
// Will be used once mcp-go supports custom request handlers
func (r *ResourceRegistry) handleUnsubscribe(ctx context.Context, request map[string]interface{}) (interface{}, error) {
	// Extract client ID and resource URI from request
	clientID, _ := request["clientId"].(string)
	uri, _ := request["uri"].(string)

	if clientID == "" || uri == "" {
		return nil, fmt.Errorf("missing clientId or uri in unsubscribe request")
	}

	// Remove subscription
	r.subscriptions.Unsubscribe(clientID, uri)

	logger.Debug(fmt.Sprintf("Client %s unsubscribed from resource: %s", clientID, uri))

	return map[string]interface{}{
		"success": true,
	}, nil
}

// Notify sends a notification to all clients subscribed to a resource URI
// Called by resource implementations when their data changes
func (r *ResourceRegistry) Notify(uri string, payload interface{}) {
	subscribers := r.subscriptions.GetSubscribers(uri)

	if len(subscribers) == 0 {
		logger.Debug(fmt.Sprintf("No subscribers for resource: %s", uri))
		return
	}

	// Create notification payload
	notification := map[string]interface{}{
		"uri": uri,
	}

	if payload != nil {
		notification["data"] = payload
	}

	// Convert to JSON for logging
	notificationJSON, _ := json.Marshal(notification)

	logger.Debug(fmt.Sprintf("Notifying %d subscribers of resource update: %s - %s",
		len(subscribers), uri, string(notificationJSON)))

	// Send notification to all subscribers
	// Note: This requires access to the MCP server's notification system
	// For now, we use the existing SendNotificationToAllClients
	// TODO: Filter by subscriber list once we have per-client notification support
	r.sendNotificationToSubscribers(subscribers, uri, notification)
}

// sendNotificationToSubscribers sends notifications to specific subscribers
func (r *ResourceRegistry) sendNotificationToSubscribers(subscribers []string, uri string, notification map[string]interface{}) {
	if r.mcpServer == nil {
		logger.Warn("MCP server not set, cannot send notifications")
		return
	}

	// TODO: When mcp-go supports per-client notifications, filter by subscribers
	// For now, send to all clients (they'll ignore if not subscribed)

	// Create the notification according to MCP spec
	notificationMethod := "notifications/resources/updated"

	// Log for debugging
	subscriberList := strings.Join(subscribers, ", ")
	logger.Debug(fmt.Sprintf("Sending %s notification to subscribers [%s]: %s", notificationMethod, subscriberList, uri))

	// Note: The actual notification sending depends on mcp-go's notification API
	// This is a placeholder for the future implementation
}

// ListRegistered returns a list of all registered resource templates
// Useful for debugging and validation
func (r *ResourceRegistry) ListRegistered() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var registered []string
	for _, res := range r.resources {
		entry := fmt.Sprintf("%s: %s", res.Definition.Name, res.Definition.UriTemplate)
		if res.Definition.Description != "" {
			entry += fmt.Sprintf(" - %s", res.Definition.Description)
		}
		registered = append(registered, entry)
	}

	return registered
}

// GetSubscriptionStats returns subscription statistics
func (r *ResourceRegistry) GetSubscriptionStats() (totalSubscriptions, uniqueClients, uniqueResources int) {
	return r.subscriptions.GetStats()
}
