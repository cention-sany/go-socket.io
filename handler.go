package socketio

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

type baseHandler struct {
	events    map[string]*caller
	name      string
	broadcast BroadcastAdaptor
	evMu      sync.Mutex
}

func newBaseHandler(name string, broadcast BroadcastAdaptor) *baseHandler {
	return &baseHandler{
		events:    make(map[string]*caller),
		name:      name,
		broadcast: broadcast,
		evMu:      sync.Mutex{},
	}
}

// On registers the function f to handle an event.
func (h *baseHandler) On(event string, f interface{}) error {
	c, err := newCaller(f)
	if err != nil {
		return err
	}
	h.evMu.Lock()
	h.events[event] = c
	h.evMu.Unlock()
	return nil
}

type socketHandler struct {
	*baseHandler
	socket *nspSocket
	rooms  map[string]struct{}
}

func newSocketHandler(ns *nspSocket, base *baseHandler) *socketHandler {
	return &socketHandler{
		baseHandler: base,
		socket:      ns,
		rooms:       make(map[string]struct{}),
	}
}

func (h *socketHandler) Rooms() []string {
	ret := make([]string, 0, len(h.rooms))
	for room := range h.rooms {
		if strings.HasPrefix(room, fmt.Sprint(h.name, ":")) {
			ret = append(ret, room)
		}
	}
	return ret
}

func (h *socketHandler) Join(room string) error {
	roomName := h.broadcastName(room)
	if err := h.baseHandler.broadcast.Join(roomName, h.socket); err != nil {
		return err
	}
	h.rooms[roomName] = struct{}{}
	return nil
}

func (h *socketHandler) Leave(room string) error {
	roomName := h.broadcastName(room)
	if err := h.baseHandler.broadcast.Leave(roomName, h.socket); err != nil {
		return err
	}
	delete(h.rooms, roomName)
	return nil
}

func (h *socketHandler) LeaveAll() error {
	for room := range h.rooms {
		if err := h.baseHandler.broadcast.Leave(h.broadcastName(room), h.socket); err != nil {
			return err
		}
	}
	return nil
}

func (h *baseHandler) BroadcastTo(room, event string, args ...interface{}) error {
	return h.broadcast.Send(nil, h.broadcastName(room), event, args...)
}

func (h *socketHandler) BroadcastTo(room, event string, args ...interface{}) error {
	return h.baseHandler.broadcast.Send(h.socket, h.broadcastName(room), event, args...)
}

func (h *baseHandler) broadcastName(room string) string {
	return fmt.Sprintf("%s:%s", h.name, room)
}

var unknownNS = errors.New("socketio: unknown namespace for on packet")

// onPacket handle the event callback On based on the incoming packet. packet
// is already been partially decode. Only packet data is not decoded.
func (h *socketHandler) onPacket(decoder *decoder, packet *packet) ([]interface{}, error) {
	var message string
	switch packet.Type {
	case _CONNECT:
		message = "connection"
	case _DISCONNECT:
		message = "disconnection"
	case _ERROR:
		message = "error"
	case _ACK:
		fallthrough
	case _BINARY_ACK:
		return nil, h.onAck(packet.Id, decoder, packet)
	default:
		if decoder != nil {
			message = decoder.Message()
		}
	}
	h.evMu.Lock()
	c, ok := h.events[message]
	h.evMu.Unlock()
	if !ok {
		// If the message is not recognized by the server, the decoder.currentCloser
		// needs to be closed otherwise the server will be stuck until the e
		if decoder != nil {
			decoder.Close()
		}
		return nil, nil
	}
	args := c.GetArgs()
	olen := len(args)
	if olen > 0 && decoder != nil {
		packet.Data = &args
		if err := decoder.DecodeData(packet); err != nil {
			return nil, err
		}
	}
	for i := len(args); i < olen; i++ {
		args = append(args, nil)
	}

	retV := c.Call(h.socket, args)
	if len(retV) == 0 {
		return nil, nil
	}

	var err error
	if last, ok := retV[len(retV)-1].Interface().(error); ok {
		err = last
		retV = retV[0 : len(retV)-1]
	}
	ret := make([]interface{}, len(retV))
	for i, v := range retV {
		ret[i] = v.Interface()
	}
	return ret, err
}

func (h *socketHandler) onAck(id int, decoder *decoder, packet *packet) error {
	h.socket.acksmu.Lock()
	c, ok := h.socket.acks[id]
	if !ok {
		h.socket.acksmu.Unlock()
		return nil
	}
	delete(h.socket.acks, id)
	h.socket.acksmu.Unlock()

	args := c.GetArgs()
	packet.Data = &args
	if err := decoder.DecodeData(packet); err != nil {
		return err
	}

	c.Call(h.socket, args)
	return nil
}
