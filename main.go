package main

import (
	"fmt"
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

	http.HandleFunc("GET /list", func(w http.ResponseWriter, r *http.Request) {
		hub.HandleHubListRequest(w, r)
	})
	http.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		HandleRequestMelody(m, w, r, nil)
	})
	m.HandleConnect(func(s *melody.Session) {
		new_session := &server.SessionInfo{
			Hub:                   hub,
			Session:               s,
			Name:                  "Player",
			ConnectionTimestampMS: server.GetUnixTimestampMS(),
		}
		hub.RegisterClient(new_session)
	})
	m.HandleDisconnect(func(s *melody.Session) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*server.SessionInfo) != nil {
			info := _info.(*server.SessionInfo)
			room := info.Room
			if room != nil {
				room.CmdChan <- server.RoomChanCmd{Id: server.ROOM_CHAN_CMD_USER_LEAVE, Session: info}
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
	fmt.Println("GoNexus Listening in 7777...")
	http.ListenAndServe(":7777", nil)
}
