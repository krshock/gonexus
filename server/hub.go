package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"text/template"
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
	ClientCount    int64
	RoomCount      int64
	Stats          HubStats
}

type HubStats struct {
	RoomCreations     int64
	RoomJoins         int64
	ClientConnections int64
}

func NewHub() *Hub {
	return &Hub{
		Mut:            sync.Mutex{},
		Rooms:          make([]*Room, 4),
		UserPacketChan: make(chan UserPacket, 32),
		CmdChan:        make(chan HubChanCmd, 32),
	}
}

var hubListTemplate = template.Must(template.ParseFiles("templates/hub_list.html"))

func (hub *Hub) HandleHubListRequest(w http.ResponseWriter, r *http.Request) {
	roomArr := make([]map[string]any, 0)
	time_now_unix := time.Now().UnixMilli()
	hub.RoomMap.Range(func(key any, value any) bool {
		room := value.(*Room)
		roomArr = append(roomArr, map[string]any{
			"Name":       room.Name,
			"AppName":    room.AppName,
			"Time":       (time_now_unix - room.CreationTimestamp) / int64(1000),
			"PacketsIn":  room.Stats.PacketsIn,
			"PacketsOut": room.Stats.PacketsOut,
			"BytesIn":    room.Stats.BytesIn,
			"BytesOut":   room.Stats.BytesOut,
		})
		return true
	})
	slices.SortFunc(roomArr, func(a, b map[string]any) int {
		return cmp.Compare[string](a["Name"].(string), b["Name"].(string))
	})
	clientsArr := make([]map[string]any, 0)
	hub.SessionMap.Range(func(k any, v any) bool {
		cli := v.(*SessionInfo)
		cliMap := map[string]any{
			"Name":       cli.Name,
			"BytesIn":    cli.Stats.BytesIn,
			"BytesOut":   cli.Stats.BytesOut,
			"PacketsIn":  cli.Stats.PacketsIn,
			"PacketsOut": cli.Stats.PacketsOut,
		}
		if cli.Room == nil {
			cliMap["RoomName"] = ""
		} else {
			cliMap["RoomName"] = cli.Room.Name
		}
		clientsArr = append(clientsArr, cliMap)
		return true
	})
	slices.SortFunc(clientsArr, func(a, b map[string]any) int {
		if a["RoomName"].(string) == b["RoomName"].(string) {
			return cmp.Compare(a["Name"].(string), b["Name"].(string))
		} else {
			return cmp.Compare(a["RoomName"].(string), b["RoomName"].(string))

		}
	})
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	sysMap := map[string]any{
		"HeapAlloc":         fmt.Sprintf("%.3f MB", ToMBf(m.HeapAlloc)),
		"TotalAlloc":        fmt.Sprintf("%.3f MB ", ToMBf(m.TotalAlloc)),
		"SysMem":            fmt.Sprintf("%.3f MB", ToMBf(m.Sys)),
		"NumGC":             m.NumGC,
		"ClientsCount":      hub.ClientCount,
		"RoomsCount":        hub.RoomCount,
		"RoomCreations":     hub.Stats.RoomCreations,
		"RoomJoins":         hub.Stats.RoomJoins,
		"ClientConnections": hub.Stats.ClientConnections,
	}

	hubListTemplate.Execute(w, map[string]any{
		"rooms":   roomArr,
		"clients": clientsArr,
		"stats":   sysMap,
	})
}

func (hub *Hub) HubGorroutine() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println(r)
			fmt.Println("stacktrace: \n" + string(debug.Stack()))
		}
	}()
	client_check_timer := time.NewTicker(1 * time.Second)
	defer client_check_timer.Stop()
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
		case <-client_check_timer.C:
			current_time := GetUnixTimestampMS()
			conn_timeout_ms := 1000
			hub.NoRoomClients.Range(func(key any, b any) bool {
				s := key.(*SessionInfo)
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
	fmt.Println("= registering client, add=", session.Session.RemoteAddr())
	hub.SessionMap.Store(session.Session, session)
	hub.NoRoomClients.Store(session, true)
	atomic.AddInt64(&hub.ClientCount, 1)
	atomic.AddInt64(&hub.Stats.ClientConnections, 1)
}

func (hub *Hub) UnregisterClient(session *SessionInfo) {
	if session.Session != nil {
		fmt.Println("= Unregistering client, name=", session.Name, " add=", session.Session.RemoteAddr())
	} else {
		fmt.Println("= Unregistering client, name=", session.Name)
	}
	if hub == nil {
		fmt.Println("= Hub nil")
		return
	}
	//fmt.Println("debug stacktrace: ", string(debug.Stack()))
	atomic.AddInt64(&hub.ClientCount, -1)
	hub.NoRoomClients.Delete(session)
	hub.SessionMap.Delete(session.Session)
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
	atomic.AddInt64(&hub.Stats.RoomJoins, 1)
	room.CmdChan <- RoomChanCmd{
		Id:      ROOM_CHAN_CMD_USER_JOIN,
		Session: session,
		RoomReq: roomReq,
	}

	return true
}

func (hub *Hub) getRandomRoomName() string {
	rand.Seed(uint64(time.Now().UnixNano()))
	ch := "0123456789abcdefghjkmnABCDEFGHJKLMN"
	for {
		rndstr := string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))])
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
		Name:              hub.getRandomRoomName(),
		Secret:            roomReq.RoomSecret,
		AppName:           roomReq.AppName,
		Peers:             make([]*SessionInfo, 4),
		Hub:               hub,
		UserPacketChan:    make(chan UserPacket, 128),
		CmdChan:           make(chan RoomChanCmd, 128),
		CreationTimestamp: time.Now().UnixMilli(),
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
	atomic.AddInt64(&hub.Stats.RoomCreations, 1)

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
			fmt.Println("create_room json: ", data)
			_ = hub.createRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	} else if msg[0] == HUB_CMD_SC_JOIN_ROOM && sessionI.Room == nil {
		json_bytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(json_bytes, &data) == nil {
			fmt.Println("join_room json: ", data)
			_ = hub.joinRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	}
}
