package main

import (
	"fmt"
	"sync"
)

const (
	ROOM_CHAN_CMD_USER_JOIN = iota
	ROOM_CHAN_CMD_USER_LEAVE
	ROOM_CHAN_CMD_ROOM_CLOSE
)

const (
	ROOM_CMD_SC_LEAVE_ROOM = iota
)

type RoomChanCmd struct {
	Id      int
	Session *SessionInfo
	RoomReq *RoomRequest
}

type Room struct {
	Mut            sync.Mutex
	Open           bool
	Id             int
	Name           string
	Secret         string
	Peers          []*SessionInfo
	Hub            *Hub
	UserPacketChan chan (UserPacket)
	CmdChan        chan (RoomChanCmd)
}

type RoomRequest struct {
	RoomId     string `json:"room_id"`
	RoomSecret string `json:"room_pwd"`
}

func (room *Room) RoomGorroutine() {
	fmt.Println("New room gorroutine ", room.Name)
	defer fmt.Println("Exiting room goroutine2 ", room.Name)

	room.Open = true
	for {
		select {
		case usrpkt := <-room.UserPacketChan:
			room.HandlePacket(usrpkt.SessionI, usrpkt.Msg)
		case cmd_ch := <-room.CmdChan:
			if cmd_ch.Id == ROOM_CHAN_CMD_USER_LEAVE {
				if cmd_ch.Session.Room.UserLeave(cmd_ch.Session, false) {
					fmt.Println("Exiting room goroutine3 ", room.Name)
					return
				}
			} else if cmd_ch.Id == ROOM_CHAN_CMD_USER_JOIN {
				room.UserJoin(cmd_ch.Session, cmd_ch.RoomReq)
			}
		}
	}
}

func (room *Room) FindUserIdx(s *SessionInfo) int {
	for idx := range room.Peers {
		if room.Peers[idx] == s {
			return idx
		}
	}
	return -1
}

func (room *Room) UserJoin(s *SessionInfo, r *RoomRequest) {
	added := false
	for idx := range room.Peers {
		if room.Peers[idx] == nil {
			room.Peers[idx] = s
			added = true
			break
		}
	}
	if added {
		s.Session.Write(buildMsgPacket(0, 0, "Ingresando a Juego:"+r.RoomId)) //Room Joined
	} else {
		s.Session.Write(buildMsgPacket(2, 0, "Juego no encontrado:"+r.RoomId)) //Room Joined
	}
}

// Unregisters session from Room, if session is room's host disconnects all clients
// and returns true to end Rooms gorroutine
func (room *Room) UserLeave(s *SessionInfo, close_conn bool) bool {
	fmt.Println("room.Userleave ", s.Session.RemoteAddr())

	if s.Room == room {
		pidx := room.FindUserIdx(s)
		if pidx > 0 {
			s.Room = nil
			room.Peers[pidx] = nil
			s.Session.Write(buildMsgPacket(2, 1, "Juego abandonado1"))
			if close_conn {
				s.Session.Close()
			}
		} else if pidx == 0 {
			room.CloseRoom(close_conn)
			return true
		}
	} else {
		s.Session.Write(buildMsgPacket(2, 0, "No hay juego activo"))
	}
	return false
}

func (room *Room) CloseRoom(close_clients bool) {
	room.Open = false
	room.Hub.RoomMap.Delete(room.Name)
	for idx, p := range room.Peers {
		if p == nil {
			continue
		}
		room.Peers[idx] = nil
		p.Room = nil
		p.Session.Write(buildMsgPacket(2, 1, "Cerrando Juegp"))
		if close_clients {
			p.Session.Close()
		}
	}

	room.Hub.CmdChan <- HubChanCmd{Id: HUB_CHAN_CMD_ROOM_UNREGISTER}
}

func (room *Room) HandlePacket(sessionI *SessionInfo, msg []byte) {
	if len(msg) == 1 && msg[0] == 2 {
		room.UserLeave(sessionI, false)
		return
	}
	fmt.Println("Invalid room packet, ", sessionI.Session.RemoteAddr())
	fmt.Println(msg)
}
