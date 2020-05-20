package hub

import (
	"github.com/raphaelreyna/pi-heater/pkg/coil"
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

type Hub struct {
	coil       *coil.Coil
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	errLog     *log.Logger
	infoLog    *log.Logger
	running    bool
	Stop       chan struct{}
	WaitGroup  *sync.WaitGroup
}

func NewHub(coil *coil.Coil, infoLog, errLog *log.Logger) *Hub {
	return &Hub{
		coil:       coil,
		infoLog:    infoLog,
		errLog:     errLog,
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		Stop:       make(chan struct{}),
	}
}

func (h *Hub) Run() {
	h.infoLog.Println("starting websocket hub run loop")
	if h.WaitGroup != nil {
		h.WaitGroup.Add(1)
	}
	h.running = true
	for h.running {
		select {
		case client := <-h.register:
			h.clients[client] = true
			h.infoLog.Printf("registered new websocket client")
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.infoLog.Printf("unregistered new websocket client")
		case frame := <-h.coil.CurrentFrameChan:
			payload, err := json.Marshal(&frame)
			if err != nil {
				panic(err)
			}
			for client := range h.clients {
				select {
				case client.send <- payload:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.infoLog.Printf("sent out frame:\n%s", string(payload))
		case <-h.Stop:
			for client := range h.clients {
				select {
				case client.send <- []byte("server process killed"):
				default:
				}
				close(client.send)
			}
			h.running = false
			h.infoLog.Println("stopped websocket hub run loop")
		}
	}
	if h.WaitGroup != nil {
		h.WaitGroup.Done()
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.errLog.Println(err)
		return
	}
	client := &Client{hub: h, conn: conn, send: make(chan []byte, 256)}
	client.hub.register <- client

	go client.writePump()
}
