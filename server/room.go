package main

import (
	"sync"

	melody "github.com/olahol/melody"
)

type SessionInfo struct {
	Hub     *Hub
	Room    *Room
	Session *melody.Session
}

const (
	ROOM_CHAN_CMD_USER_LEAVE = 1
)

type Room struct {
	Mut            sync.Mutex
	Id             int
	Name           string
	Secret         string
	Peers          []*SessionInfo
	Hub            *Hub
	UserPacketChan chan (UserPacket)
	CmdChan        chan (int)
}

type RoomChanCmd struct {
	Id      int
	Session *SessionInfo
}

type UserPacket struct {
	Msg      []byte
	SessionI *SessionInfo
}

type RoomRequest struct {
	RoomId     string `json:"room_id"`
	RoomSecret string `json:"room_pwd"`
}

func (room *Room) RoomGorroutine() {
	room.Hub.Wg.Add(1)
	defer room.Hub.Wg.Done()
	for {
		select {
		case usrpkt := <-room.UserPacketChan:
			room.HandlePacket(usrpkt.SessionI, usrpkt.Msg)
		}
	}
}

func (room *Room) HandlePacket(sessionI *SessionInfo, msg []byte) {

}
