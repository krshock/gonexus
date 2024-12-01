package main

import (
	"fmt"
	"sync"
	"time"
)

const (
	ROOM_CHAN_CMD_SEND_PACKET = iota
	ROOM_CHAN_CMD_USER_JOIN
	ROOM_CHAN_CMD_USER_LEAVE
	ROOM_CHAN_CMD_ROOM_CLOSE
)

const (
	ROOM_CMD_PEER_PACKET_SEND = iota
	ROOM_CMD_LEAVE_ROOM
	ROOM_CMD_TOOGLE_JOIN
)

type RoomChanCmd struct {
	Id           int
	PacketTarget int
	Msg          []byte
	Session      *SessionInfo
	RoomReq      *RoomRequest
}

type Room struct {
	Mut            sync.Mutex
	Open           bool
	Id             int
	Name           string
	Secret         string
	AppName        string
	Peers          []*SessionInfo
	Hub            *Hub
	UserPacketChan chan (UserPacket)
	CmdChan        chan (RoomChanCmd)
	AllowJoin      bool
}

type RoomRequest struct {
	RoomId     string `json:"room_id"`
	RoomSecret string `json:"room_pwd"`
	AppName    string `json:"app_name"`
	PlayerName string `json:"player_name"`
}

func (room *Room) RoomGorroutine() {
	fmt.Println("New room gorroutine ", room.Name)
	defer fmt.Println("Exiting room goroutine ", room.Name)

	room.Open = true
	for {
		select {
		case usrpkt := <-room.UserPacketChan:
			room.HandlePacket(usrpkt.SessionI, usrpkt.Msg)
		case cmd_ch := <-room.CmdChan:
			if cmd_ch.Id == ROOM_CHAN_CMD_SEND_PACKET {

			} else if cmd_ch.Id == ROOM_CHAN_CMD_USER_LEAVE {
				if cmd_ch.Session.Room.UserLeave(cmd_ch.Session, true) {
					return
				}
			} else if cmd_ch.Id == ROOM_CHAN_CMD_USER_JOIN {
				room.UserJoin(cmd_ch.Session, cmd_ch.RoomReq)
			}
		}
	}
}

func buildUserPacket(ori uint8, dst uint8, msg []byte) []byte {
	b := []byte{1, 0, ori, dst}
	b = append(b, msg...)
	return b
}

func (room *Room) SendPacket(ori uint8, dst uint8, msg []byte, except_peer uint8) {
	if !room.Open {
		return
	}
	if dst == 255 {
		for idx, p := range room.Peers {
			if p == nil || ori == uint8(idx) || except_peer == uint8(idx) {
				continue
			}
			p.SendPacket(msg)
		}
		return
	} else if int(dst) < len(room.Peers) {
		if room.Peers[dst] != nil {
			//fmt.Println("Packet sent: tgt=", dst, " msg=", msg)
			room.Peers[dst].SendPacket(msg)
		} else {
			fmt.Println("SendPacket: Invalid DST peer_id=", dst)
		}
	} else {
		fmt.Println("Sendpacket: Invalid dst, ORI=", ori, " DST=", dst)
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
	peer_id := 0
	for idx := range room.Peers {
		if room.Peers[idx] == nil {
			room.Peers[idx] = s
			peer_id = idx
			added = true
			break
		}
	}

	if added {
		s.Room = room
		s.PeerId = peer_id
		s.Name = r.PlayerName
		s.Hub.NoRoomClients.Delete(s)
		s.SendPacket(buildMsgPacket(0, 0, "Ingresando a Juego:"+r.RoomId)) //Room Joining

		s.SendPacket(buildPlayerPacket(uint8(s.PeerId), 2, s.Name))
		room.SendPacket(uint8(s.PeerId), 255, buildPlayerPacket(uint8(s.PeerId), 1, s.Name), uint8(peer_id))

		for _, p := range room.Peers {
			if p == nil || p == s {
				continue
			}
			s.SendPacket(buildPlayerPacket(uint8(p.PeerId), 1, p.Name))
		}

		s.SendPacket(buildMsgPacket(5, 0, r.RoomId)) //Room Joined

	} else {
		s.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado:"+r.RoomId)) //Room Not JOined
	}
}

// Unregisters session from Room, if session is room's host disconnects all clients
// and returns true to end Rooms gorroutine
func (room *Room) UserLeave(s *SessionInfo, unregister_session bool) bool {
	fmt.Println("room.Userleave ", s.Session.RemoteAddr())

	if s.Room == room {
		pidx := room.FindUserIdx(s)
		if pidx > 0 {
			s.Room = nil

			room.Peers[pidx] = nil

			s.SendPacket(buildMsgPacket(2, 1, "Juego abandonado"))
			room.SendPacket(255, 255, buildPlayerPacket(uint8(pidx), 0, s.Name), 255)

			if unregister_session {
				go func() {
					time.Sleep(1 * time.Second)
					if s.Session != nil && !s.Session.IsClosed() {
						s.Session.Close()
					}
					s.Hub.UnregisterClient(s)
				}()
			}
		} else if pidx == 0 {
			room.closeRoom(true)
			return true
		}
	} else {
		s.SendPacket(buildMsgPacket(2, 0, "No hay juego activo"))
	}
	return false
}

func (room *Room) closeRoom(unregister_sessions bool) {
	fmt.Println("room.CloseRoom ", room.Name)
	room.Open = false

	for idx, p := range room.Peers {
		if p == nil {
			continue
		}
		room.Peers[idx] = nil
		p.Room = nil
		p.SendPacket(buildMsgPacket(2, 1, "Cerrando Juego"))
		if unregister_sessions {
			go func() {
				time.Sleep(1 * time.Second)
				if p.Session != nil && !p.Session.IsClosed() {
					p.Session.Close()
				}
				p.Hub.UnregisterClient(p)
			}()
		}
	}

	room.Hub.CmdChan <- HubChanCmd{Id: HUB_CHAN_CMD_ROOM_UNREGISTER, Room: room}
}

func (room *Room) HandlePacket(sessionI *SessionInfo, msg []byte) {
	if len(msg) > 4 && msg[0] == ROOM_CMD_PEER_PACKET_SEND {
		//fmt.Println("Peer packet: ", msg)
		msg[1] = byte(sessionI.PeerId) //Origin field is written in server, not client
		//fmt.Println("peer packet, origin=", msg[1], "target=", msg[2])

		if !sessionI.IsHost && msg[2] != 0 {
			fmt.Println("Non host can only send packets to the host ori=", msg[1], " dst=", msg[2], " packet=", msg)
			return
		}
		room.SendPacket(msg[1], msg[2], buildUserPacket(msg[1], msg[2], msg[4:]), msg[3])
		return

	} else if len(msg) == 1 && msg[0] == ROOM_CMD_LEAVE_ROOM {
		//room.UserLeave(sessionI, false)
		fmt.Println("Leave Packet: ", msg)
		room.CmdChan <- RoomChanCmd{Id: ROOM_CHAN_CMD_USER_LEAVE, Session: sessionI}
		return
	} else if len(msg) == 2 && msg[0] == ROOM_CMD_TOOGLE_JOIN && sessionI.IsHost {
		sessionI.SendPacket(buildMsgPacket(111, 0, "allowjoin toogle"))
		room.AllowJoin = msg[1] != 0
		return
	}
	fmt.Println("Invalid room packet, ", sessionI.Session.RemoteAddr())
	fmt.Println(msg)
}
