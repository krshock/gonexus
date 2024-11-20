package main

import (
	"encoding/json"
	"fmt"
	"sync"
)

const (
	HUB_CMD_SC_CREATE_ROOM = iota
	HUB_CMD_SC_JOIN_ROOM
)

const (
	HUB_CHAN_CMD_ROOM_UNREGISTER = iota
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
		}
	}
}

func (hub *Hub) JoinRoomRequest(session *SessionInfo, roomReq *RoomRequest) bool {
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

func (hub *Hub) CreateRoom(session *SessionInfo, roomReq *RoomRequest) *Room {
	_r, _ := hub.RoomMap.Load(roomReq.RoomId)
	if _r != nil {
		session.SendPacket(buildMsgPacket(2, 2, "Juego Ya Creado:"+roomReq.RoomId))
		return nil
	}
	new_room := &Room{
		Name:           roomReq.RoomId,
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
	hub.Mut.Lock()
	defer hub.Mut.Unlock()
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

	hub.RoomMap.Store(roomReq.RoomId, new_room)
	fmt.Println("Room created: name=", new_room.Name, " secret=", new_room.Secret)
	go new_room.RoomGorroutine()
	session.SendPacket(buildMsgPacket(0, 0, "Ingresando a Juego:"+new_room.Name)) //Room Joined
	session.SendPacket(buildPlayerPacket(uint8(0), 2, session.Name))
	return new_room
}

func (hub *Hub) HandlePacket(sessionI *SessionInfo, msg []byte) {
	//fmt.Println("Hub Packet In ", sessionI.Session.RemoteAddr(), " -> ", msg)
	if msg[0] == HUB_CMD_SC_CREATE_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("json recieved", data)
			_ = hub.CreateRoom(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	} else if msg[0] == HUB_CMD_SC_JOIN_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("json recieved", data)
			_ = hub.JoinRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	}
}
