package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	RoomChanCmdSendPacket = iota
	RoomChanCmdUserJoin
	RoomChanCmdUserLeave
	RoomChanCmdRoomClose
)

const (
	RoomCmdPeerPacketSend = iota
	RoomCmdLeaveRoom
	RoomCmdToogleJoin
)

type RoomChanCmd struct {
	ID           int
	PacketTarget int
	Msg          []byte
	Session      *SessionInfo
	RoomReq      *RoomRequest
}

type Room struct {
	Mut               sync.Mutex
	Open              bool
	ID                int
	Name              string
	Secret            string
	AppName           string
	Peers             []*SessionInfo
	Hub               *Hub
	UserPacketChan    chan (UserPacket)
	CmdChan           chan (RoomChanCmd)
	AllowJoin         bool
	Stats             RoomStats
	CreationTimestamp int64
}

type RoomStats struct {
	PacketsIn  int64
	PacketsOut int64
	BytesIn    int64
	BytesOut   int64
}

type RoomRequest struct {
	RoomID     string `json:"room_id"`
	RoomSecret string `json:"room_pwd"`
	AppName    string `json:"app_name"`
	PlayerName string `json:"player_name"`
}

func (room *Room) RoomGorroutine() {
	fmt.Println("New room gorroutine ", room.Name)
	defer fmt.Println("Exiting room goroutine ", room.Name)

	atomic.AddInt64(&room.Hub.RoomCount, 1)
	defer atomic.AddInt64(&room.Hub.RoomCount, -1)

	room.Open = true
	for {
		select {
		case usrpkt := <-room.UserPacketChan:
			room.HandlePacket(usrpkt.SessionI, usrpkt.Msg)
		case cmdCh := <-room.CmdChan:
			switch cmdCh.ID {
			case RoomChanCmdSendPacket:
			case RoomChanCmdUserJoin:
				room.UserJoin(cmdCh.Session, cmdCh.RoomReq)
			case RoomChanCmdUserLeave:
				if cmdCh.Session.Room.UserLeave(cmdCh.Session, true) {
					return
				}
			}
		}
	}
}

func buildUserPacket(ori uint8, dst uint8, msg []byte) []byte {
	b := []byte{1, 0, ori, dst}
	b = append(b, msg...)
	return b
}

func (room *Room) SendPacket(ori uint8, dst uint8, msg []byte, exceptPeer uint8) {
	if !room.Open {
		return
	}
	if dst == 255 {
		for idx, p := range room.Peers {
			if p == nil || ori == uint8(idx) || exceptPeer == uint8(idx) {
				continue
			}
			p.SendPacket(msg)
			atomic.AddInt64(&room.Stats.PacketsOut, 1)
			atomic.AddInt64(&room.Stats.BytesOut, int64(len(msg)))
		}
		return
	} else if int(dst) < len(room.Peers) {
		if room.Peers[dst] != nil {
			room.Peers[dst].SendPacket(msg)
			atomic.AddInt64(&room.Stats.PacketsOut, 1)
			atomic.AddInt64(&room.Stats.BytesOut, int64(len(msg)))
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
	peerID := 0
	for idx := range room.Peers {
		if room.Peers[idx] == nil {
			room.Peers[idx] = s
			peerID = idx
			added = true
			break
		}
	}

	if added {
		s.Room = room
		s.PeerID = peerID
		s.Name = r.PlayerName
		s.Hub.NoRoomClients.Delete(s)
		s.SendPacket(buildMsgPacket(0, 0, "Ingresando a Juego:"+r.RoomID)) // Room Joining

		s.SendPacket(buildPlayerPacket(uint8(s.PeerID), 2, s.Name))
		room.SendPacket(uint8(s.PeerID), 255, buildPlayerPacket(uint8(s.PeerID), 1, s.Name), uint8(peerID))

		for _, p := range room.Peers {
			if p == nil || p == s {
				continue
			}
			s.SendPacket(buildPlayerPacket(uint8(p.PeerID), 1, p.Name))
		}

		s.SendPacket(buildMsgPacket(5, 0, r.RoomID)) // Room Joined

	} else {
		s.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado:"+r.RoomID)) // Room Not JOined
	}
}

// UserLeave , Unregisters session from Room, if session is room's host disconnects all clients
// and returns true to end Rooms gorroutine
func (room *Room) UserLeave(s *SessionInfo, unregisterSession bool) bool {
	fmt.Println("room.Userleave ", s.Session.RemoteAddr())

	if s.Room == room {
		pidx := room.FindUserIdx(s)
		if pidx > 0 {
			s.Room = nil

			room.Peers[pidx] = nil

			s.SendPacket(buildMsgPacket(2, 1, "Juego abandonado"))
			room.SendPacket(255, 255, buildPlayerPacket(uint8(pidx), 0, s.Name), 255)

			if unregisterSession {
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

func (room *Room) closeRoom(unregisterSessions bool) {
	fmt.Println("room.CloseRoom ", room.Name)
	room.Open = false

	for idx, p := range room.Peers {
		if p == nil {
			continue
		}
		room.Peers[idx] = nil
		p.Room = nil
		p.SendPacket(buildMsgPacket(2, 1, "Cerrando Juego"))
		if unregisterSessions {
			go func() {
				time.Sleep(1 * time.Second)
				if p.Session != nil && !p.Session.IsClosed() {
					p.Session.Close()
				}
				p.Hub.UnregisterClient(p)
			}()
		}
	}

	room.Hub.CmdChan <- HubChanCmd{ID: HubChanCmdRoomUnregister, Room: room}
}

func (room *Room) HandlePacket(sessionI *SessionInfo, msg []byte) {
	atomic.AddInt64(&room.Stats.PacketsIn, 1)
	atomic.AddInt64(&room.Stats.BytesIn, int64(len(msg)))

	if len(msg) > 4 && msg[0] == RoomCmdPeerPacketSend {
		msg[1] = byte(sessionI.PeerID) // Origin field is written in server, not client
		if !sessionI.IsHost && msg[2] != 0 {
			fmt.Println("Non host can only send packets to the host ori=", msg[1], " dst=", msg[2], " packet=", string(msg))
			return
		}
		room.SendPacket(msg[1], msg[2], buildUserPacket(msg[1], msg[2], msg[4:]), msg[3])
		return
	} else if len(msg) == 1 && msg[0] == RoomCmdLeaveRoom {
		fmt.Println("Leave Packet: ", string(msg))
		room.CmdChan <- RoomChanCmd{ID: RoomChanCmdUserLeave, Session: sessionI}
		return
	} else if len(msg) == 2 && msg[0] == RoomCmdToogleJoin && sessionI.IsHost {
		sessionI.SendPacket(buildMsgPacket(111, 0, "allowjoin toogle"))
		room.AllowJoin = msg[1] != 0
		return
	}
	fmt.Println("Invalid room packet, ", sessionI.Session.RemoteAddr())
	fmt.Println(string(msg))
}
