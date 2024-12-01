package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	"golang.org/x/exp/rand"
)

const (
	HUB_CMD_SC_CREATE_ROOM = iota
	HUB_CMD_SC_JOIN_ROOM
)

const (
	HUB_CHAN_CMD_ROOM_UNREGISTER = iota
	HUB_CHAN_CMD_NEW_CLIENT
)

type HubChanCmd struct {
	Id      int
	Session *SessionInfo
	Room    *Room
	IntVal  int
}

type Hub struct {
	Mut            sync.Mutex
	Rooms          []*Room
	RoomMap        sync.Map
	Wg             sync.WaitGroup
	UserPacketChan chan (UserPacket)
	CmdChan        chan (HubChanCmd)
	SessionMap     sync.Map
	NoRoomClients  sync.Map
}

func NewHub() *Hub {
	return &Hub{
		Mut:            sync.Mutex{},
		Rooms:          make([]*Room, 4),
		UserPacketChan: make(chan UserPacket, 32),
		CmdChan:        make(chan HubChanCmd, 32),
	}
}

func (hub *Hub) HubGorroutine() {
	memcheck_timer := time.NewTicker(30 * time.Second)
	client_check_timer := time.NewTicker(1 * time.Second)
	defer memcheck_timer.Stop()
	unreg_cli_count := uint64(0)
	cli_count := uint64(0)
	room_count := uint64(0)
	for {
		select {
		case usrpck := <-hub.UserPacketChan:
			hub.HandlePacket(usrpck.SessionI, usrpck.Msg)
		case chanmsg := <-hub.CmdChan:
			if chanmsg.Id == HUB_CHAN_CMD_ROOM_UNREGISTER {
				//free resources from hub
				hub.RoomMap.Delete(chanmsg.Room.Name)
				hub.Rooms[chanmsg.Room.Id] = nil
			}
		case <-memcheck_timer.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Printf("HeapAlloc = %.3f MB ", ToMBf(m.HeapAlloc))
			fmt.Printf("TotalAlloc = %.3f MB ", ToMBf(m.TotalAlloc))
			fmt.Printf("Sys = %.3f MB ", ToMBf(m.Sys))
			fmt.Printf("NumGC = %v\n", m.NumGC)
			fmt.Printf("rooms: %v ", room_count)
			fmt.Printf("clients: %v ", cli_count)
			fmt.Printf("unreg_clients: %v \n", unreg_cli_count)
		case <-client_check_timer.C:
			current_time := GetUnixTimestampMS()
			conn_timeout_ms := 1000
			unreg_cli_count = 0
			cli_count = 0
			hub.SessionMap.Range(func(k any, b any) bool {
				cli_count++
				return true
			})
			room_count = 0
			hub.RoomMap.Range(func(k any, b any) bool {
				room_count++
				return true
			})
			hub.NoRoomClients.Range(func(key any, b any) bool {
				s := key.(*SessionInfo)
				unreg_cli_count++
				if s.ConnectionTimestampMS+uint64(conn_timeout_ms) >= current_time {
					if s.Room != nil {
						hub.NoRoomClients.Delete(s)
					} else {
						s.Session.Close()
					}
				}
				return true
			})
		}
	}

}

func (hub *Hub) RegisterClient(session *SessionInfo) {
	hub.NoRoomClients.Store(session, true)
}

func (hub *Hub) UnregisterClient(session *SessionInfo) {
	if hub == nil {
		return
	}
	hub.NoRoomClients.Delete(session)
	hub.SessionMap.Delete(session)
	session.Room = nil
	session.Hub = nil
	session.Session = nil
}

func ToMBf(val uint64) float64 {
	return float64(val) / 1024.0 / 1024.0
}

func (hub *Hub) joinRoomRequest(session *SessionInfo, roomReq *RoomRequest) bool {
	//
	if roomReq.RoomId == "" || session.Room != nil {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado:"+roomReq.RoomId))
		return false
	}
	value, _ := hub.RoomMap.Load(roomReq.RoomId)
	if value == nil || value.(*Room) == nil {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado:"+roomReq.RoomId))
		return false
	}
	room := value.(*Room)

	if room.AppName != roomReq.AppName {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado(Version incompatible):"+roomReq.RoomId))
		return false
	}
	if !room.AllowJoin {
		session.SendPacket(buildMsgPacket(111, 0, "No se aceptan nuevos jugadores:"+roomReq.RoomId))
		return false
	}

	if room.Secret != roomReq.RoomSecret {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado(Contraseña inválida):"+roomReq.RoomId))
		return false
	}

	if !room.Open {
		session.SendPacket(buildMsgPacket(2, 1, "Juego se encuentra cerrado:"+roomReq.RoomId))
		return false
	}

	room.CmdChan <- RoomChanCmd{
		Id:      ROOM_CHAN_CMD_USER_JOIN,
		Session: session,
		RoomReq: roomReq,
	}

	return true
}

func (hub *Hub) get_random_room_name() string {
	ch := "0123456789"
	for {
		rndstr := string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))])
		_, ok := hub.RoomMap.Load(rndstr)
		if !ok {
			return rndstr
		}
	}
}

func (hub *Hub) createRoomRequest(session *SessionInfo, roomReq *RoomRequest) *Room {
	if roomReq.RoomSecret == "" {
		session.SendPacket(buildMsgPacket(2, 2, "Es necesaria una clave"))
		return nil
	}
	_r, _ := hub.RoomMap.Load(roomReq.RoomId)
	if _r != nil {
		session.SendPacket(buildMsgPacket(2, 2, "Juego Ya Creado:"+roomReq.RoomId))
		return nil
	}
	new_room := &Room{
		Name:           hub.get_random_room_name(),
		Secret:         roomReq.RoomSecret,
		AppName:        roomReq.AppName,
		Peers:          make([]*SessionInfo, 4),
		Hub:            hub,
		UserPacketChan: make(chan UserPacket, 128),
		CmdChan:        make(chan RoomChanCmd, 128),
	}
	new_room.Peers[0] = session
	hub.Rooms = append(hub.Rooms, new_room)

	//Must be called from hub corroutine, if it deadlocks is because
	added := false
	for idx := range hub.Rooms {
		if hub.Rooms[idx] == nil {
			hub.Rooms[idx] = new_room
			added = true
			break
		}
	}
	if !added {
		session.SendPacket(buildMsgPacket(2, 111, "Maxima capacidad de juegos simultaneos"))
		return nil
	}

	session.Room = new_room
	session.IsHost = true
	session.PeerId = 0
	session.Name = roomReq.PlayerName
	hub.NoRoomClients.Delete(session)

	hub.RoomMap.Store(new_room.Name, new_room)

	fmt.Println("Room created: name=", new_room.Name, " secret=", new_room.Secret)
	go new_room.RoomGorroutine()
	session.SendPacket(buildMsgPacket(0, 0, new_room.Name)) //Room Joining
	session.SendPacket(buildPlayerPacket(uint8(0), 2, session.Name))
	session.SendPacket(buildMsgPacket(5, 0, new_room.Name)) //Room Joined

	return new_room
}

func (hub *Hub) HandlePacket(sessionI *SessionInfo, msg []byte) {
	//fmt.Println("Hub Packet In ", sessionI.Session.RemoteAddr(), " -> ", msg)
	if msg[0] == HUB_CMD_SC_CREATE_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("json recieved", data)
			_ = hub.createRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	} else if msg[0] == HUB_CMD_SC_JOIN_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("json recieved", data)
			_ = hub.joinRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	}
}
