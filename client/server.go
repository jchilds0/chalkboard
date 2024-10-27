package client

import (
	"bufio"
	"chalkboard/canvas"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	NONE = iota
	GET_ROOM
)

type Request struct {
	Type int
	Name string
}

type Response struct {
	Room *canvas.Canvas
}

func sendMessage[T any](conn net.Conn, m T) (err error) {
	buf, err := json.Marshal(m)
	if err != nil {
		return
	}

	_, err = conn.Write(buf)
	if err != nil {
		return
	}

	_, err = conn.Write([]byte{0})
	return
}

func recvMessage[T any](conn net.Conn) (m T, err error) {
	buf, err := bufio.NewReader(conn).ReadBytes(0)
	if err != nil {
		return
	}

	err = json.Unmarshal(buf[:len(buf)-1], &m)
	return
}

type Server struct {
	ps  *pubsub.PubSub
	ctx context.Context
	id  peer.ID

	lock  sync.Mutex
	Addr  net.Addr
	Rooms map[string]*canvas.Canvas
}

func NewServer(ps *pubsub.PubSub, ctx context.Context, id peer.ID) (*Server, error) {
	s := &Server{
		ps:    ps,
		ctx:   ctx,
		id:    id,
		Rooms: make(map[string]*canvas.Canvas, 128),
	}

	socket, err := net.Listen("tcp", "")
	if err != nil {
		return nil, err
	}

	s.Addr = socket.Addr()
	go s.listen(socket)

	return s, nil
}

func (s *Server) CreateRoom(owner, room string) (err error) {
	if _, ok := s.Rooms[room]; ok {
		return fmt.Errorf("Room %s exists", room)
	}
	name := fmt.Sprintf("/%s/%s", owner, room)

	topic, err := s.ps.Join(name)
	if err != nil {
		return err
	}

	c, err := canvas.NewCanvas(owner, room, topic, s.ctx, s.id)
	if err != nil {
		return err
	}

	s.Rooms[room] = c
	log.Println("Create room", owner, room)
	return nil
}

func (s *Server) GetRoom(name string) (c *canvas.Canvas, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	c, ok := s.Rooms[name]
	if !ok {
		err = fmt.Errorf("Room %s does not exist", name)
	}

	return
}

func (s *Server) listen(server net.Listener) {
	defer server.Close()

	for {
		client, err := server.Accept()
		if err != nil {
			log.Println("Error connecting to client:", err)
			continue
		}

		go s.handleConn(client)
	}
}

func (s *Server) handleConn(client net.Conn) {
	defer func() {
		client.Close()
		log.Println(client.RemoteAddr().String(), "Closed Connection")
	}()

	for {
		req, err := recvMessage[Request](client)
		if err != nil {
			log.Println(client.RemoteAddr().String(), "\t", err)
			return
		}

		switch req.Type {
		case NONE:
			continue

		case GET_ROOM:
			c, err := s.GetRoom(req.Name)
			if err != nil {
				log.Println(client.RemoteAddr(), "\t", "Error sending response", err)
				continue
			}

			err = sendMessage(client, Response{Room: c})
			if err != nil {
				log.Println(client.RemoteAddr(), "\t", "Error sending response", err)
				continue
			}

		default:
			log.Println(client.RemoteAddr(), "\t", "Unknown request type", req.Type)
			continue
		}
	}
}
