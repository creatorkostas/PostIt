package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultWebSocketReadTimeout is the maximum time to wait for a message before closing
const DefaultWebSocketReadTimeout = 60 * time.Second

// WebSocket configuration constants
const (
	MaxWSMessages       = 1000              // Maximum messages to keep in buffer
	wsHandshakeTimeout  = 10 * time.Second  // WebSocket dial timeout
	wsCloseNormal       = 1000              // Normal closure code
)

type WSMessage struct {
	Type      string    `json:"type"` // "sent", "received", "error"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type WSClient struct {
	Conn     *websocket.Conn
	Messages []WSMessage
	mu       sync.Mutex
	connMu   sync.Mutex
	cancel   context.CancelFunc
	connGen  atomic.Int64 // incremented on each Connect to detect stale goroutines
}

func NewWSClient() *WSClient {
	return &WSClient{
		Messages: make([]WSMessage, 0),
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Same-origin or no Origin header (e.g., from a tool)
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		
		uHost := u.Hostname()
		return uHost == "localhost" || uHost == "127.0.0.1" || uHost == host
	},
}

func (c *WSClient) Connect(url string) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	// 1. Cancel previous listener if it exists
	if c.cancel != nil {
		c.cancel()
		if c.Conn != nil {
			c.Conn.Close()
		}
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}
	
	c.Conn = conn
	c.connGen.Add(1)
	myGen := c.connGen.Load()
	
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	
	// Start listener
	go func(ctx context.Context, gen int64) {
		currentConn := conn
		defer func() {
			currentConn.Close()
			c.connMu.Lock()
			if c.connGen.Load() == gen {
				c.Conn = nil
			}
			c.connMu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Set read deadline to prevent hanging on unresponsive servers
				currentConn.SetReadDeadline(time.Now().Add(DefaultWebSocketReadTimeout))

				_, message, err := currentConn.ReadMessage()
				if err != nil {
					// Check if it's a timeout
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						c.addMessage("error", "read timeout")
						return
					}
					// Check if it's a normal closure or if we cancelled it
					select {
					case <-ctx.Done():
						return
					default:
						c.addMessage("error", err.Error())
						return
					}
				}
				c.addMessage("received", string(message))
			}
		}
	}(ctx, myGen)
	
	return nil
}

func (c *WSClient) Send(message string) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.Conn == nil {
		return fmt.Errorf("not connected")
	}
	err := c.Conn.WriteMessage(websocket.TextMessage, []byte(message))
	if err != nil {
		c.addMessage("error", err.Error())
		return err
	}
	c.addMessage("sent", message)
	return nil
}

func (c *WSClient) addMessage(msgType, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Prevent unbounded memory growth with circular buffer
	if len(c.Messages) >= MaxWSMessages {
		c.Messages = c.Messages[1:] // Remove oldest message
	}
	c.Messages = append(c.Messages, WSMessage{
		Type:      msgType,
		Content:   content,
		Timestamp: time.Now(),
	})
}

func (c *WSClient) GetMessages() []WSMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Messages
}

func (c *WSClient) Close() {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.Conn != nil {
		c.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.Conn.Close()
		c.Conn = nil
	}
}
