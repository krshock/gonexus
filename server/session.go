package server

import (
	"sync/atomic"

	"github.com/olahol/melody"
)

type SessionInfo struct {
	PeerId                int
	Session               *melody.Session
	Room                  *Room
	Hub                   *Hub
	Name                  string
	IsHost                bool
	ConnectionTimestampMS uint64
	Stats                 Stats
	UniqueId              string
}

type Stats struct {
	PacketsIn  int64
	PacketsOut int64
	BytesIn    int64
	BytesOut   int64
}

func (s *SessionInfo) SendPacket(msg []byte) {
	if s.Session != nil {
		s.Session.WriteBinary(msg)
		atomic.AddInt64(&s.Stats.PacketsOut, 1)
		atomic.AddInt64(&s.Stats.BytesOut, int64(len(msg)))
	}
}

func (s *SessionInfo) RecvPacket(msg []byte) {
	atomic.AddInt64(&s.Stats.PacketsIn, 1)
	atomic.AddInt64(&s.Stats.BytesIn, int64(len(msg)))

	if msg[0] == 1 && s.Room != nil {
		s.Room.UserPacketChan <- UserPacket{SessionI: s, Msg: msg[1:]}
		return
	} else if msg[0] == 0 {
		s.Hub.UserPacketChan <- UserPacket{SessionI: s, Msg: msg[1:]}
		return
	}
}

type UserPacket struct {
	Msg      []byte
	SessionI *SessionInfo
}

func buildMsgPacket(subcmd uint8, msgid uint8, msg string) []byte {
	b := []byte{0, 0, 0}
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
	b := []byte{1, 3, 0, 0}
	b[2] = playerId
	b[3] = state
	if name != "" {
		b = append(b, []byte(name)...)
	}
	return b
}
