package main

import (
	"log"
	"net/http"

	"github.com/krshock/mob84hub/server"
	melody "github.com/olahol/melody"
)

func HandleRequestMelody(m *melody.Melody, w http.ResponseWriter, r *http.Request, keys map[string]any) error {
	return m.HandleRequestWithKeys(w, r, nil)
}

func main() {
	m := melody.New()
	m.Upgrader.CheckOrigin = func(r *http.Request) bool { return true }

	hub := server.NewHub()
	go hub.HubGorroutine()

	http.HandleFunc("GET /nexus/list", func(w http.ResponseWriter, r *http.Request) {
		hub.HandleHubListRequest(w, r)
	})
	http.HandleFunc("GET /nexus/ws", func(w http.ResponseWriter, r *http.Request) {
		HandleRequestMelody(m, w, r, nil)
	})
	m.HandleConnect(func(s *melody.Session) {
		newSession := &server.SessionInfo{
			Hub:                   hub,
			Session:               s,
			Name:                  "Player",
			ConnectionTimestampMS: server.GetUnixTimestampMS(),
		}
		hub.RegisterClient(newSession)
	})
	m.HandleDisconnect(func(s *melody.Session) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*server.SessionInfo) != nil {
			info := _info.(*server.SessionInfo)
			room := info.Room
			if room != nil {
				room.CmdChan <- server.RoomChanCmd{ID: server.RoomChanCmdUserLeave, Session: info}
			} else {
				hub.UnregisterClient(info)
			}
		}
	})
	m.HandleMessageBinary(func(s *melody.Session, msg []byte) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*server.SessionInfo) != nil {
			info := _info.(*server.SessionInfo)
			info.RecvPacket(msg)
		}
	})
	log.Println("GoNexus Listening in 7777...")
	http.ListenAndServe("127.0.0.1:7777", nil)
}
