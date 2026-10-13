package model

import "time"

// WebSocket message types
const (
	WSMessageTypeProgress = "progress"
	WSMessageTypeComplete = "complete"
	WSMessageTypeError    = "error"
	WSMessageTypePing     = "ping"
	WSMessageTypePong     = "pong"
)

// WSMessage represents a generic WebSocket message
type WSMessage struct {
	Type string `json:"type"`
}

// WSProgressMessage represents a progress update
type WSProgressMessage struct {
	Type        string    `json:"type"`
	JobID       string    `json:"jobId"`
	Progress    int       `json:"progress"`
	Status      JobStatus `json:"status"`
	CurrentStep string    `json:"currentStep,omitempty"`
}

// WSCompleteMessage represents job completion
type WSCompleteMessage struct {
	Type   string      `json:"type"`
	JobID  string      `json:"jobId"`
	Result interface{} `json:"result"`
}

// WSErrorMessage represents an error
type WSErrorMessage struct {
	Type  string  `json:"type"`
	JobID string  `json:"jobId"`
	Error WSError `json:"error"`
}

// WSError represents error details
type WSError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WSTicketRequest asks for a ticket to connect to a job's WebSocket stream.
type WSTicketRequest struct {
	JobID string `json:"jobId" validate:"required,uuid"`
}

// WSTicketResponse: the ticket is single-use and valid until ExpiresAt.
// The client uses it with GET /ws/jobs/{jobId}?ticket=<ticket>.
type WSTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
}
