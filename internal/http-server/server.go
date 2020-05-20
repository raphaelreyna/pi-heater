package server

import (
	"github.com/raphaelreyna/pi-heater/pkg/coil"
	"github.com/raphaelreyna/pi-heater/internal/websocket-hub"
	"encoding/json"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"strconv"
)

type Server struct {
	router  *mux.Router
	coil    *coil.Coil
	hub     *hub.Hub
	errLog  *log.Logger
	infoLog *log.Logger
}

func NewServer(coil *coil.Coil, hub *hub.Hub, errLog, infoLog *log.Logger) *Server {
	s := &Server{
		coil:    coil,
		hub:     hub,
		errLog:  errLog,
		infoLog: infoLog,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.router = mux.NewRouter()
	s.router.HandleFunc("/", s.handleGet()).Methods("GET")
	s.router.HandleFunc("/", s.handlePost()).Methods("POST")
	s.router.HandleFunc("/ws", s.hub.ServeHTTP)
}

func (s *Server) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		frame := s.coil.CurrentFrame
		payload, err := json.Marshal(&frame)
		if err != nil {
			s.errLog.Printf("error while marshaling JSON for frame: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "application/json")
		w.Write(payload)
	}
}

func (s *Server) handlePost() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetString := r.URL.Query().Get("target")
		target, err := strconv.ParseFloat(targetString, 64)
		if err != nil {
			s.errLog.Printf("error while parsing target from URL: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.coil.SetTarget <- target
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}
