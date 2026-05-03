package protocol

// Engine defines the universal contract for any UI automation backend
type Engine interface {
	// Initialize prepares the environment (e.g., opening a URL for web, or launching an app for Android)
	Initialize(req InitializeRequest) error

	// Observe returns the current spatial tree and visual context
	Observe() (*ObservationResponse, error)

	// ExecuteAction performs a physical interaction
	ExecuteAction(req ActionRequest) error

	// Close cleans up resources (closes Chrome, kills ADB port forwards, etc.)
	Close() error
}
