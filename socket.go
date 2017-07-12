package socketio

import (
	"net/http"
	"sync"

	"github.com/googollee/go-engine.io"
)

// Socket is the socket object of socket.io.
type Socket interface {

	// Id returns the session id of socket.
	Id() string

	// Rooms returns the rooms name joined now.
	Rooms() []string

	// Request returns the first http request when established connection.
	Request() *http.Request

	// On registers the function f to handle an event.
	On(event string, f interface{}) error

	// Emit emits an event with given args.
	Emit(event string, args ...interface{}) error

	// Join joins the room.
	Join(room string) error

	// Leave leaves the room.
	Leave(room string) error

	// Disconnect disconnect the socket.
	Disconnect()

	// BroadcastTo broadcasts an event to the room with given args.
	BroadcastTo(room, event string, args ...interface{}) error
}

type socket struct {
	// shouldn't need protection as its write only access by socket.loop once
	// during socket creation.
	nsps   map[string]*nspSocket
	conn   engineio.Conn
	id     int
	mu     sync.Mutex
	acks   map[int]*caller
	acksmu sync.Mutex
}

func newSocket(conn engineio.Conn, ns *namespace) *socket {
	nss := map[string]*nspSocket{}
	ret := &socket{
		conn: conn,
		acks: make(map[int]*caller),
	}
	for k, v := range ns.root {
		nss[k] = newNspSocket(ret, v.baseHandler)
	}
	ret.nsps = nss
	return ret
}

func (s *socket) Id() string {
	return s.conn.Id()
}

func (s *socket) Request() *http.Request {
	return s.conn.Request()
}

func (s *socket) Disconnect() {
	s.conn.Close()
}

func (s *socket) namespace(nsp string) *nspSocket {
	n := s.nsps[nsp]
	if n == nil {
		// fallback to default namespace
		n = s.nsps[""]
	}
	return n
}

func (s *socket) loop() (err error) {
	defer func() {
		for k, v := range s.nsps {
			if v.disconnected {
				continue
			}
			v.LeaveAll()
			// trigger disconnect event on all namespaces
			p := packet{
				Type: _DISCONNECT,
				Id:   -1,
				NSP:  k,
			}
			v.onPacket(nil, &p)
			v.disconnected = true
		}
	}()

	p := packet{
		Type: _CONNECT,
		Id:   -1,
	}
	encoder := newEncoder(s.conn)
	if err = encoder.Encode(p); err != nil {
		return
	}
	s.namespace("").onPacket(nil, &p) // use default namespace (server's)
	for {
		decoder := newDecoder(s.conn)
		var p packet
		if err = decoder.Decode(&p); err != nil {
			return
		}
		ns := s.namespace(p.NSP)
		var ret []interface{}
		ret, err = ns.onPacket(decoder, &p)
		if err != nil {
			return
		}
		switch p.Type {
		case _CONNECT:
			ns.sendConnect()
		case _BINARY_EVENT:
			fallthrough
		case _EVENT:
			if p.Id >= 0 {
				p := packet{
					Type: _ACK,
					Id:   p.Id,
					NSP:  p.NSP,
					Data: ret,
				}
				encoder := newEncoder(s.conn)
				if err = encoder.Encode(p); err != nil {
					return
				}
			}
		case _DISCONNECT:
			ns.LeaveAll()
			ns.disconnected = true
		}
	}
}
