package websocket

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/makeasinger/api/internal/model"
)

// Client represents a WebSocket client
type Client struct {
	JobID string
	Conn  *websocket.Conn
	Send  chan []byte
}

// Hub maintains active WebSocket connections
type Hub struct {
	// Clients grouped by job ID
	clients map[string]map[*Client]bool

	// Register requests
	register chan *Client

	// Unregister requests
	unregister chan *Client

	// Broadcast messages to job subscribers
	broadcast chan *BroadcastMessage

	mu sync.RWMutex
}

// BroadcastMessage represents a message to broadcast
type BroadcastMessage struct {
	JobID   string
	Message []byte
}

// NewHub creates a new Hub
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *BroadcastMessage, 256),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if h.clients[client.JobID] == nil {
				h.clients[client.JobID] = make(map[*Client]bool)
			}
			h.clients[client.JobID][client] = true
			h.mu.Unlock()
			log.Printf("Client registered for job %s", client.JobID)

		case client := <-h.unregister:
			h.mu.Lock()
			if clients, ok := h.clients[client.JobID]; ok {
				if _, ok := clients[client]; ok {
					delete(clients, client)
					close(client.Send)
					if len(clients) == 0 {
						delete(h.clients, client.JobID)
					}
				}
			}
			h.mu.Unlock()
			log.Printf("Client unregistered from job %s", client.JobID)

		case msg := <-h.broadcast:
			h.mu.RLock()
			if clients, ok := h.clients[msg.JobID]; ok {
				for client := range clients {
					select {
					case client.Send <- msg.Message:
					default:
						close(client.Send)
						delete(clients, client)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Register adds a new client
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// BroadcastProgress sends a progress update to all job subscribers
func (h *Hub) BroadcastProgress(jobID string, progress int, status model.JobStatus, step string) {
	msg := model.WSProgressMessage{
		Type:        model.WSMessageTypeProgress,
		JobID:       jobID,
		Progress:    progress,
		Status:      status,
		CurrentStep: step,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal progress message: %v", err)
		return
	}

	h.broadcast <- &BroadcastMessage{
		JobID:   jobID,
		Message: data,
	}
}

// BroadcastComplete sends a completion message to all job subscribers
func (h *Hub) BroadcastComplete(jobID string, result interface{}) {
	msg := model.WSCompleteMessage{
		Type:   model.WSMessageTypeComplete,
		JobID:  jobID,
		Result: result,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal complete message: %v", err)
		return
	}

	h.broadcast <- &BroadcastMessage{
		JobID:   jobID,
		Message: data,
	}
}

// BroadcastError sends an error message to all job subscribers
func (h *Hub) BroadcastError(jobID string, code, message string) {
	msg := model.WSErrorMessage{
		Type:  model.WSMessageTypeError,
		JobID: jobID,
		Error: model.WSError{
			Code:    code,
			Message: message,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal error message: %v", err)
		return
	}

	h.broadcast <- &BroadcastMessage{
		JobID:   jobID,
		Message: data,
	}
}

// HandleConnection handles a WebSocket connection
func (h *Hub) HandleConnection(c *websocket.Conn, jobID string) {
	client := &Client{
		JobID: jobID,
		Conn:  c,
		Send:  make(chan []byte, 256),
	}

	h.Register(client)
	defer h.Unregister(client)

	// Start writer goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case message, ok := <-client.Send:
				if !ok {
					c.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				if err := c.WriteMessage(websocket.TextMessage, message); err != nil {
					return
				}

			case <-ticker.C:
				// Send ping for keep-alive
				if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Reader loop
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Handle client messages (ping/pong)
		var msg model.WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		if msg.Type == model.WSMessageTypePing {
			pong := model.WSMessage{Type: model.WSMessageTypePong}
			data, _ := json.Marshal(pong)
			client.Send <- data
		}
	}
}
