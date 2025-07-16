// Package server
package server

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
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

// Hub contains all clients and room information of a server. Wg (WaitingGroup) is
// not used because the function HubGorroutine keeps running while the hub is active to process
// events
type Hub struct {
	Mut            sync.Mutex
	RoomMap        sync.Map
	Wg             sync.WaitGroup
	UserPacketChan chan (UserPacket)
	CmdChan        chan (HubChanCmd)
	SessionMap     sync.Map
	NoRoomClients  sync.Map
	ClientCount    int64
	RoomCount      int64
	Stats          HubStats
	SessionIds     sync.Map
}

// HubStats stores stats about the
type HubStats struct {
	RoomCreations     int64
	RoomJoins         int64
	ClientConnections int64
}

// IDs for network packets processed by the hub
const (
	HubCmdScCreateRoom = iota
	HubCmdSJoinRoom
)

// Ids for commands sent using hub.CmdChan channel
const (
	HubChanCmdRoomUnregister = iota
	HubChanCmdNewClient
)

// HubChanCmd contains parameters for the hub event channel read inside the function HubGorroutine
type HubChanCmd struct {
	ID      int
	Session *SessionInfo
	Room    *Room
	IntVal  int
}

func NewHub() *Hub {
	return &Hub{
		Mut:            sync.Mutex{},
		UserPacketChan: make(chan UserPacket, 32),
		CmdChan:        make(chan HubChanCmd, 32),
	}
}

//go:embed hubstats.html
var hubListTemplateSource string

var hubListTemplate = template.Must(template.New("Name").Parse(hubListTemplateSource))

// HandleHubListRequest , System Info http request handler. Lists server stats, memory, rooms and client data
func (hub *Hub) HandleHubListRequest(w http.ResponseWriter, r *http.Request) {
	roomArr := make([]map[string]any, 0)
	timeNowUnix := time.Now().UnixMilli()
	hub.RoomMap.Range(func(key any, value any) bool {
		room := value.(*Room)
		roomArr = append(roomArr, map[string]any{
			"Name":       room.Name,
			"AppName":    room.AppName,
			"Time":       (timeNowUnix - room.CreationTimestamp) / int64(1000),
			"PacketsIn":  room.Stats.PacketsIn,
			"PacketsOut": room.Stats.PacketsOut,
			"BytesIn":    room.Stats.BytesIn,
			"BytesOut":   room.Stats.BytesOut,
		})
		return true
	})
	slices.SortFunc(roomArr, func(a, b map[string]any) int {
		return cmp.Compare(a["Name"].(string), b["Name"].(string))
	})
	clientsArr := make([]map[string]any, 0)
	hub.SessionMap.Range(func(k any, v any) bool {
		cli := v.(*SessionInfo)
		cliMap := map[string]any{
			"UniqueId":   cli.UniqueID,
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

// HubGorroutine ,Main hub event handler. All hub members can be modified in this loop as a thread-safety
// constraint to keep concurrency bugs away.
func (hub *Hub) HubGorroutine() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println(r)
			fmt.Println("stacktrace: \n" + string(debug.Stack()))
		}
	}()
	clientCheckTimer := time.NewTicker(1 * time.Second)
	defer clientCheckTimer.Stop()
	for {
		select {
		case usrpck := <-hub.UserPacketChan:
			hub.HandlePacket(usrpck.SessionI, usrpck.Msg)
		case chanmsg := <-hub.CmdChan:
			if chanmsg.ID == HubChanCmdRoomUnregister {
				// free resources from hub
				hub.RoomMap.Delete(chanmsg.Room.Name)
			}
		case <-clientCheckTimer.C:
			currentTime := GetUnixTimestampMS()
			connTimeoutMs := 1000
			hub.NoRoomClients.Range(func(key any, b any) bool {
				s := key.(*SessionInfo)
				if s.ConnectionTimestampMS+uint64(connTimeoutMs) >= currentTime {
					if s.Room != nil {
						hub.NoRoomClients.Delete(s)
					} else {
						log.Printf("hub:client_check_timer: Closing session: %v", s.UniqueID)
						s.Session.Close()
						// s.Close(1000, "Action timeout")
					}
				}
				return true
			})
		}
	}
}

// RegisterClient , Registers a client connection as a hub's session
func (hub *Hub) RegisterClient(session *SessionInfo) {
	fmt.Println("= registering client, add=", session.Session.RemoteAddr())
	hub.SessionMap.Store(session.Session, session)
	hub.NoRoomClients.Store(session, true)
	atomic.AddInt64(&hub.ClientCount, 1)
	atomic.AddInt64(&hub.Stats.ClientConnections, 1)
	hub.setRandomClientID(session)
}

// UnregisterClient a client connection in the hub
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
	// fmt.Println("debug stacktrace: ", string(debug.Stack()))
	atomic.AddInt64(&hub.ClientCount, -1)
	hub.NoRoomClients.Delete(session)
	hub.SessionMap.Delete(session.Session)
	hub.SessionIds.Delete(session)
	session.Room = nil
	session.Hub = nil
	session.Session = nil
}

func ToMBf(val uint64) float64 {
	return float64(val) / 1024.0 / 1024.0
}

// Processes a roomRequest struct to join a client to a room.
func (hub *Hub) joinRoomRequest(session *SessionInfo, roomReq *RoomRequest) bool {
	//
	if roomReq.RoomID == "" || session.Room != nil {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado:"+roomReq.RoomID))
		return false
	}
	value, _ := hub.RoomMap.Load(roomReq.RoomID)
	if value == nil || value.(*Room) == nil {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado:"+roomReq.RoomID))
		return false
	}
	room := value.(*Room)

	if room.AppName != roomReq.AppName {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado(Version incompatible):"+roomReq.RoomID))
		return false
	}
	if !room.AllowJoin {
		session.SendPacket(buildMsgPacket(111, 0, "No se aceptan nuevos jugadores:"+roomReq.RoomID))
		// session.Close(101, "No new players Accepted")
		return false
	}

	if room.Secret != roomReq.RoomSecret {
		session.SendPacket(buildMsgPacket(2, 0, "Juego no encontrado(Contraseña inválida):"+roomReq.RoomID))
		// session.Close(100, "Room/Password invalid")
		return false
	}

	if !room.Open {
		session.SendPacket(buildMsgPacket(2, 1, "Juego se encuentra cerrado:"+roomReq.RoomID))
		// session.Close(100, "Game is closed")
		return false
	}
	atomic.AddInt64(&hub.Stats.RoomJoins, 1)
	room.CmdChan <- RoomChanCmd{
		ID:      RoomChanCmdUserJoin,
		Session: session,
		RoomReq: roomReq,
	}

	return true
}

// Creates and registers random new room name in the hub
func (hub *Hub) getRandomRoomName(room *Room) {
	ch := "0123456789abcdefghjkmnABCDEFGHJKLMN"
	for {
		rand.Seed(uint64(time.Now().UnixNano()))
		rndstr := string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))])
		// Here comes the threadsafety
		if _, loaded := hub.RoomMap.LoadOrStore(rndstr, room); !loaded {
			room.Name = rndstr
			return
		}
	}
}

// setRandomClientID , Creates and registers random new client UniqueId in the hub
func (hub *Hub) setRandomClientID(conn *SessionInfo) {
	ch := "0123456789abcdefghjkmnABCDEFGHJKLMN"
	for {
		rand.Seed(uint64(time.Now().UnixNano()))
		rndstr := string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))]) + string(ch[rand.Intn(len(ch))])
		if _, loaded := hub.SessionIds.LoadOrStore(rndstr, conn); !loaded {
			conn.UniqueID = rndstr
			return
		}
	}
}

// Processes a roomRequest of room creation, creates a room in the hub
func (hub *Hub) createRoomRequest(session *SessionInfo, roomReq *RoomRequest) *Room {
	if roomReq.RoomSecret == "" {
		session.SendPacket(buildMsgPacket(2, 2, "Es necesaria una clave"))
		// session.Close(200, "Password required")
		return nil
	}
	_r, _ := hub.RoomMap.Load(roomReq.RoomID)
	if _r != nil {
		session.SendPacket(buildMsgPacket(2, 2, "Juego Ya Creado:"+roomReq.RoomID))
		// session.Close(201, "Juego ya existe")
		return nil
	}
	newRoom := &Room{
		Secret:            roomReq.RoomSecret,
		AppName:           roomReq.AppName,
		Peers:             make([]*SessionInfo, 4),
		Hub:               hub,
		UserPacketChan:    make(chan UserPacket, 128),
		CmdChan:           make(chan RoomChanCmd, 128),
		CreationTimestamp: time.Now().UnixMilli(),
	}
	newRoom.Peers[0] = session

	hub.getRandomRoomName(newRoom)
	session.Room = newRoom
	session.IsHost = true
	session.PeerID = 0
	session.Name = roomReq.PlayerName
	hub.NoRoomClients.Delete(session)

	hub.RoomMap.Store(newRoom.Name, newRoom)
	atomic.AddInt64(&hub.Stats.RoomCreations, 1)

	fmt.Println("Room created: name=", newRoom.Name, " secret=", newRoom.Secret)
	go newRoom.RoomGorroutine()
	session.SendPacket(buildMsgPacket(0, 0, newRoom.Name)) // Room Joining
	session.SendPacket(buildPlayerPacket(uint8(0), 2, session.Name))
	session.SendPacket(buildMsgPacket(5, 0, newRoom.Name)) // Room Joined

	return newRoom
}

// HandlePacket receives packets. Must be called from the hub corroutine to conform to the
// concurrency model
func (hub *Hub) HandlePacket(sessionI *SessionInfo, msg []byte) {
	if msg[0] == HubCmdScCreateRoom && sessionI.Room == nil {
		jsonBytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(jsonBytes, &data) == nil {
			fmt.Println("create_room json: ", data)
			_ = hub.createRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	} else if msg[0] == HubCmdSJoinRoom && sessionI.Room == nil {
		jsonBytes := msg[1:]
		data := RoomRequest{}
		if json.Unmarshal(jsonBytes, &data) == nil {
			fmt.Println("join_room json: ", data)
			_ = hub.joinRoomRequest(sessionI, &data)
		} else {
			fmt.Println("Invalid json recieved")
		}
	}
}
