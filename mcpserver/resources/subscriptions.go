package resources

import (
	"sync"
)

// SubscriptionTracker manages client subscriptions to resources
// Thread-safe tracking of which clients are subscribed to which resource URIs
type SubscriptionTracker struct {
	mu sync.RWMutex
	// subscriptions maps resource URI -> list of client IDs
	// Example: "diagnostics://file:///path/to/file.ts" -> ["client-1", "client-2"]
	subscriptions map[string][]string
	// clientSubscriptions maps client ID -> list of resource URIs
	// Used for cleanup when a client disconnects
	// Example: "client-1" -> ["diagnostics://file:///foo.ts", "lsp-server://gopls/status"]
	clientSubscriptions map[string][]string
}

// NewSubscriptionTracker creates a new subscription tracker
func NewSubscriptionTracker() *SubscriptionTracker {
	return &SubscriptionTracker{
		subscriptions:       make(map[string][]string),
		clientSubscriptions: make(map[string][]string),
	}
}

// Subscribe adds a client subscription to a resource URI
func (st *SubscriptionTracker) Subscribe(clientID, uri string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Add to resource -> clients mapping
	clients := st.subscriptions[uri]
	// Check if already subscribed
	for _, existingClientID := range clients {
		if existingClientID == clientID {
			return // Already subscribed
		}
	}
	st.subscriptions[uri] = append(clients, clientID)

	// Add to client -> resources mapping
	resources := st.clientSubscriptions[clientID]
	st.clientSubscriptions[clientID] = append(resources, uri)
}

// Unsubscribe removes a client subscription from a resource URI
func (st *SubscriptionTracker) Unsubscribe(clientID, uri string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Remove from resource -> clients mapping
	clients := st.subscriptions[uri]
	filtered := make([]string, 0, len(clients))
	for _, existingClientID := range clients {
		if existingClientID != clientID {
			filtered = append(filtered, existingClientID)
		}
	}
	if len(filtered) > 0 {
		st.subscriptions[uri] = filtered
	} else {
		delete(st.subscriptions, uri)
	}

	// Remove from client -> resources mapping
	resources := st.clientSubscriptions[clientID]
	filteredResources := make([]string, 0, len(resources))
	for _, existingURI := range resources {
		if existingURI != uri {
			filteredResources = append(filteredResources, existingURI)
		}
	}
	if len(filteredResources) > 0 {
		st.clientSubscriptions[clientID] = filteredResources
	} else {
		delete(st.clientSubscriptions, clientID)
	}
}

// GetSubscribers returns the list of client IDs subscribed to a resource URI
func (st *SubscriptionTracker) GetSubscribers(uri string) []string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	clients := st.subscriptions[uri]
	// Return a copy to prevent external modification
	result := make([]string, len(clients))
	copy(result, clients)
	return result
}

// UnsubscribeAll removes all subscriptions for a client (e.g., when client disconnects)
func (st *SubscriptionTracker) UnsubscribeAll(clientID string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Get all resources this client is subscribed to
	resources := st.clientSubscriptions[clientID]

	// Remove client from all resource subscriptions
	for _, uri := range resources {
		clients := st.subscriptions[uri]
		filtered := make([]string, 0, len(clients))
		for _, existingClientID := range clients {
			if existingClientID != clientID {
				filtered = append(filtered, existingClientID)
			}
		}
		if len(filtered) > 0 {
			st.subscriptions[uri] = filtered
		} else {
			delete(st.subscriptions, uri)
		}
	}

	// Remove client subscription tracking
	delete(st.clientSubscriptions, clientID)
}

// GetClientSubscriptions returns all resource URIs a client is subscribed to
func (st *SubscriptionTracker) GetClientSubscriptions(clientID string) []string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	resources := st.clientSubscriptions[clientID]
	// Return a copy to prevent external modification
	result := make([]string, len(resources))
	copy(result, resources)
	return result
}

// GetStats returns subscription statistics for debugging/monitoring
func (st *SubscriptionTracker) GetStats() (totalSubscriptions, uniqueClients, uniqueResources int) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	uniqueResources = len(st.subscriptions)
	uniqueClients = len(st.clientSubscriptions)

	for _, clients := range st.subscriptions {
		totalSubscriptions += len(clients)
	}

	return totalSubscriptions, uniqueClients, uniqueResources
}
