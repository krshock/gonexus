package main

import (
	"encoding/json"
	"fmt"
	"sync"
)

const (
	HUB_CMDP_CREATE_ROOM = iota
	HUB_CMDP_JOIN_ROOM
)

type HubChanCmd struct {
	Id     int
	Si     *SessionInfo
	IntVal int
}

type Hub struct {
	Mut            sync.Mutex
	Rooms          []*Room
	RoomMap        sync.Map
	Wg             sync.WaitGroup
	UserPacketChan chan (UserPacket)
	CmdChan        chan (int)
}

func NewHub() Hub {
	return Hub{
		Mut:            sync.Mutex{},
		Rooms:          make([]*Room, 32),
		UserPacketChan: make(chan UserPacket),
		CmdChan:        make(chan int),
	}
}

func (hub *Hub) HubGorroutine() {
	for {
		select {
		case usrpck := <-hub.UserPacketChan:
			hub.HandlePacket(usrpck.SessionI, usrpck.Msg)
		}
	}
}

func (hub *Hub) JoinRoom(session *SessionInfo, room_name string, room_secret string) bool {
	if session.Room != nil {
		return false
	}
	value, ok := hub.RoomMap.Load(room_name)
	if !ok {
		return false
	}
	room := value.(*Room)
	room.Mut.Lock()
	defer room.Mut.Unlock()
	for idx, _ := range room.Peers {
		if room.Peers[idx] == nil {
			room.Peers[idx] = session
			break
		}
	}
	return true
}

func (hub *Hub) CreateRoom(session *SessionInfo, name string, secret string) *Room {
	new_room := Room{
		Name:   name,
		Secret: secret,
		Peers:  make([]*SessionInfo, 32),
		Hub:    hub,
	}
	new_room.Peers[0] = session
	hub.Rooms = append(hub.Rooms, &new_room)

	//Must be called from hub corroutine, if it deadlocks is because
	hub.Mut.Lock()
	defer hub.Mut.Unlock()
	added := false
	for idx := range hub.Rooms {
		if hub.Rooms[idx] == nil {
			hub.Rooms[idx] = &new_room
			added = true
			break
		}
	}
	if !added {
		return nil
	}
	hub.RoomMap.Store(name, &new_room)
	fmt.Println("Room created: name=", new_room.Name, " secret=", new_room.Secret)
	session.Session.Write([]byte{1, 0, 0})
	return &new_room
}

func (hub *Hub) HandlePacket(sessionI *SessionInfo, msg []byte) {
	fmt.Println("Hub Packet In ", sessionI.Session.RemoteAddr(), " -> ", msg)
	if msg[0] == HUB_CMDP_CREATE_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("json recieved", data)
			_ = hub.CreateRoom(sessionI, data.RoomId, data.RoomSecret)
		}
	} else if msg[0] == HUB_CMDP_JOIN_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("json recieved", data)
			_ = hub.JoinRoom(sessionI, data.RoomId, data.RoomSecret)
		}
	}
}
