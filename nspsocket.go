// Socket with namespace support.

package socketio

import "reflect"

type nspSocket struct {
	*socketHandler
	*socket
	// only connected flag is needed as this flag is view from client to server
	// and default leave it as zero value false.
	connected bool
}

func newNspSocket(s *socket, base *baseHandler) *nspSocket {
	ns := &nspSocket{
		socket: s,
	}
	ns.socketHandler = newSocketHandler(ns, base)
	return ns
}

func (n *nspSocket) Emit(event string, args ...interface{}) error {
	if err := n.nspEmit(event, args...); err != nil {
		return err
	}
	if event == "disconnect" {
		n.Disconnect()
	}
	return nil
}

func (n *nspSocket) nspEmit(event string, args ...interface{}) error {
	var c *caller
	if l := len(args); l > 0 {
		fv := reflect.ValueOf(args[l-1])
		if fv.Kind() == reflect.Func {
			var err error
			c, err = newCaller(args[l-1])
			if err != nil {
				return err
			}
			args = args[:l-1]
		}
	}
	args = append([]interface{}{event}, args...)
	if c != nil {
		id, err := n.sendId(args)
		if err != nil {
			return err
		}
		n.acksmu.Lock()
		n.acks[id] = c
		n.acksmu.Unlock()
		return nil
	}
	return n.send(args)
}

func (n *nspSocket) Disconnect() {
	if n.name != "" {
		n.sendDisconnect()
		return
	}
	n.socket.Disconnect()
}

func (n *nspSocket) sendDisconnect() error {
	packet := packet{
		Type: _DISCONNECT,
		Id:   -1,
		NSP:  n.name,
	}
	encoder := newEncoder(n.conn)
	return encoder.Encode(packet)
}

func (n *nspSocket) send(args []interface{}) error {
	packet := packet{
		Type: _EVENT,
		Id:   -1,
		NSP:  n.name,
		Data: args,
	}
	encoder := newEncoder(n.conn)
	return encoder.Encode(packet)
}

// sendConnect sends connection event to client. This event always trigger from
// client as server is always the listening party waiting for accept connection.
// sendConnect basically send back the callback to client that use connect.
func (n *nspSocket) sendConnect() error {
	packet := packet{
		Type: _CONNECT,
		Id:   -1,
		NSP:  n.name,
	}
	n.connected = true
	encoder := newEncoder(n.conn)
	return encoder.Encode(packet)
}

func (n *nspSocket) sendId(args []interface{}) (int, error) {
	n.mu.Lock()
	packet := packet{
		Type: _EVENT,
		Id:   n.id,
		NSP:  n.name,
		Data: args,
	}
	n.id++
	if n.id < 0 {
		n.id = 0
	}
	n.mu.Unlock()

	encoder := newEncoder(n.conn)
	err := encoder.Encode(packet)
	if err != nil {
		return -1, nil
	}
	return packet.Id, nil
}
