package main

import (
	"fmt"
	"net/http"

	melody "github.com/olahol/melody"
)

type SessionInfo struct {
	PeerId                int
	Session               *melody.Session
	Room                  *Room
	Hub                   *Hub
	Name                  string
	IsHost                bool
	ConnectionTimestampMS uint64
}

func (s *SessionInfo) SendPacket(msg []byte) {
	if s.Session != nil {
		s.Session.WriteBinary(msg)
	}
}

func (s *SessionInfo) RecvPacket(msg []byte) {
	if msg[0] == 1 && s.Room != nil {
		s.Room.UserPacketChan <- UserPacket{SessionI: s, Msg: msg[1:]}
		return
	} else if msg[0] == 0 {
		s.Hub.UserPacketChan <- UserPacket{SessionI: s, Msg: msg[1:]}
		return
	} else if msg[0] == 5 {
		fmt.Println("Echoing msg to ", s.Session.RemoteAddr())
		s.SendPacket(msg)
		return
	}
}

type UserPacket struct {
	Msg      []byte
	SessionI *SessionInfo
}

func buildMsgPacket(subcmd uint8, msgid uint8, msg string) []byte {
	var b = []byte{0, 0, 0}
	b[0] = 2
	b[1] = subcmd
	b[2] = msgid
	if msg != "" {
		strb := []byte(msg)
		b = append(b, strb...)
	}
	return b
}

func buildPlayerPacket(playerId uint8, state uint8, name string) []byte {
	var b = []byte{1, 3, 0, 0}
	b[2] = playerId
	b[3] = state
	if name != "" {
		b = append(b, []byte(name)...)
	}
	return b
}

func HandleRequestMelody(m *melody.Melody, w http.ResponseWriter, r *http.Request, keys map[string]any) error {
	return m.HandleRequestWithKeys(w, r, nil)
}

func main() {
	m := melody.New()
	m.Upgrader.CheckOrigin = func(r *http.Request) bool { return true }

	hub := NewHub()
	go hub.HubGorroutine()

	http.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		//fmt.Println("Web request from ", r.RemoteAddr)
		HandleRequestMelody(m, w, r, nil)
	})
	m.HandleConnect(func(s *melody.Session) {
		//fmt.Println("New Connection ", s.Request.RemoteAddr)
		new_session := &SessionInfo{
			Hub:                   hub,
			Session:               s,
			Name:                  "Player",
			ConnectionTimestampMS: GetUnixTimestampMS(),
			//DelayMs: 75,
		}
		hub.RegisterClient(new_session)
	})
	m.HandleDisconnect(func(s *melody.Session) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*SessionInfo) != nil {
			info := _info.(*SessionInfo)
			room := info.Room
			if room != nil {
				room.CmdChan <- RoomChanCmd{Id: ROOM_CHAN_CMD_USER_LEAVE, Session: info}
			} else {
				hub.UnregisterClient(info)
			}
		}
	})
	m.HandleMessageBinary(func(s *melody.Session, msg []byte) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*SessionInfo) != nil {
			info := _info.(*SessionInfo)
			info.RecvPacket(msg)
		}
	})
	fmt.Println("GoNexus Listening in 7777...")
	http.ListenAndServe(":7777", nil)
}
