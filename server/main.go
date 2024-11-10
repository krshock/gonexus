package main

import (
	"fmt"
	"net/http"

	melody "github.com/olahol/melody"
)

func HandleRequestMelody(m *melody.Melody, w http.ResponseWriter, r *http.Request, keys map[string]any) error {
	return m.HandleRequestWithKeys(w, r, nil)
}

func main() {
	m := melody.New()
	m.Upgrader.CheckOrigin = func(r *http.Request) bool { return true }

	hub := NewHub()
	go hub.HubGorroutine()

	http.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Web request from ", r.RemoteAddr)
		HandleRequestMelody(m, w, r, nil)
	})
	m.HandleConnect(func(s *melody.Session) {
		fmt.Println("New Connection ", s.Request.RemoteAddr)
		s.Keys = map[string]any{
			"info": &SessionInfo{
				Hub:     &hub,
				Room:    nil,
				Session: s,
			},
		}
	})
	m.HandleMessageBinary(func(s *melody.Session, msg []byte) {
		if s.Keys["info"] != nil {
			info := s.Keys["info"].(*SessionInfo)
			if info.Room != nil && msg[0] == 1 {
				info.Room.UserPacketChan <- UserPacket{SessionI: info, Msg: msg[1:]}
				return
			} else if info.Hub != nil && msg[0] == 0 {
				hub.UserPacketChan <- UserPacket{SessionI: info, Msg: msg[1:]}
				return
			} else if msg[0] == 5 {
				fmt.Println("Echoing msg to ", s.RemoteAddr())
				s.Write(msg)
				return
			}
		}
		s.CloseWithMsg([]byte("Invalid Packet"))
	})
	fmt.Println("GoNexus Listening in 7777...")
	http.ListenAndServe(":7777", nil)
}
