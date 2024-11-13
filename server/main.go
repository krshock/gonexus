package main

import (
	"fmt"
	"net/http"

	melody "github.com/olahol/melody"
)

type SessionInfo struct {
	PeerId  int
	Session *melody.Session
	Room    *Room
	Hub     *Hub
	Name    string
	IsHost  bool
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

func buildPLayerPacket(pid uint8, state uint8, name string) []byte {
	var b = []byte{1, 3, 0, 0}
	b[2] = pid
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
		hub.SessionMap.Store(s, &SessionInfo{
			Hub:     hub,
			Session: s,
		})
	})
	m.HandleDisconnect(func(s *melody.Session) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*SessionInfo) != nil {
			info := _info.(*SessionInfo)
			room := info.Room
			hub.SessionMap.Delete(s)
			if room != nil {
				room.CmdChan <- RoomChanCmd{Id: ROOM_CHAN_CMD_USER_LEAVE, Session: info}
			}
		}
	})
	m.HandleMessageBinary(func(s *melody.Session, msg []byte) {
		_info, _ := hub.SessionMap.Load(s)
		if _info.(*SessionInfo) != nil {
			info := _info.(*SessionInfo)
			if msg[0] == 1 && info.Room != nil {
				info.Room.UserPacketChan <- UserPacket{SessionI: info, Msg: msg[1:]}
				return
			} else if msg[0] == 0 {
				hub.UserPacketChan <- UserPacket{SessionI: info, Msg: msg[1:]}
				return
			} else if msg[0] == 5 {
				fmt.Println("Echoing msg to ", s.RemoteAddr())
				s.Write(msg)
				return
			}
			fmt.Println("invalid packet: ", info.Session.Request.RemoteAddr)
			fmt.Println(msg)
		}
	})
	fmt.Println("GoNexus Listening in 7777...")
	http.ListenAndServe(":7777", nil)
}
