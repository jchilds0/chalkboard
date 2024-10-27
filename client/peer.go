package client

import (
	"chalkboard/canvas"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	OWNER_NAME = iota
	ROOM_NAME
)

type Peer struct {
	Name   string
	Host   host.Host
	ctx    context.Context
	ps     *pubsub.PubSub
	Server *Server
	peers  map[string]net.Conn
	Rooms  *gtk.ListStore
	iters  map[string]map[string]*gtk.TreeIter
}

func NewPeer(name string, h host.Host, ctx context.Context, ps *pubsub.PubSub) (*Peer, error) {
	host := &Peer{
		Name:  name,
		Host:  h,
		ctx:   ctx,
		ps:    ps,
		peers: make(map[string]net.Conn, 128),
		iters: make(map[string]map[string]*gtk.TreeIter, 128),
	}

	var err error
	host.Rooms, err = gtk.ListStoreNew(glib.TYPE_STRING, glib.TYPE_STRING)
	if err != nil {
		return nil, err
	}

	host.Server, err = NewServer(ps, ctx, h.ID())
	if err != nil {
		return nil, err
	}

	go host.listenRooms()

	return host, nil
}

func (host *Peer) JoinRoom(ownerName, roomName string) (c *canvas.Canvas, err error) {
	if ownerName == host.Name {
		// our room
		c, err = host.Server.GetRoom(roomName)
		return
	}

	conn, ok := host.peers[ownerName]
	if !ok {
		err = fmt.Errorf("Unknown peer %s", ownerName)
		return
	}

	err = sendMessage(conn, Request{Type: GET_ROOM, Name: roomName})
	if err != nil {
		return
	}

	res, err := recvMessage[Response](conn)
	if err != nil {
		return
	}

	name := fmt.Sprintf("/%s/%s", ownerName, roomName)
	topic, err := host.ps.Join(name)
	if err != nil {
		return nil, err
	}

	err = res.Room.InitCanvas(topic, host.ctx, host.Host.ID())
	if err != nil {
		return nil, err
	}
	return res.Room, nil
}

// HandlePeerFound connects to peers discovered via mDNS. Once they're connected,
// the PubSub system will automatically start interacting with them if they also
// support PubSub.
func (host *Peer) HandlePeerFound(pi peer.AddrInfo) {
	fmt.Printf("discovered new peer %s\n", pi.ID)
	err := host.Host.Connect(context.Background(), pi)
	if err != nil {
		fmt.Printf("error connecting to peer %s: %s\n", pi.ID, err)
	}
}

type Client struct {
	Name  string
	Addr  string
	Rooms []string
}

func (host *Peer) AddRoomRow(owner, room string) {
	if _, ok := host.iters[owner]; !ok {
		host.iters[owner] = make(map[string]*gtk.TreeIter, 128)
	}

	rows := host.iters[owner]

	if _, ok := rows[room]; !ok {
		rows[room] = host.Rooms.Append()
	}

	iter := rows[room]

	host.Rooms.SetValue(iter, OWNER_NAME, owner)
	host.Rooms.SetValue(iter, ROOM_NAME, room)
}

func (host *Peer) listenRooms() {
	roomTopic, err := host.ps.Join("/client")
	if err != nil {
		log.Fatal(err)
	}

	roomSub, err := roomTopic.Subscribe()
	if err != nil {
		log.Fatal()
	}

	hostClient := Client{
		Name:  host.Name,
		Addr:  host.Server.Addr.String(),
		Rooms: make([]string, 0, 128),
	}

	// Recieve rooms for other hosts
	go func() {
		for {
			msg, err := roomSub.Next(host.ctx)
			if err != nil {
				log.Println("Error listening for peers:", err)
				continue
			}

			// ignore our own messages
			if msg.ReceivedFrom == host.Host.ID() {
				continue
			}

			var client Client
			err = json.Unmarshal(msg.Data, &client)
			if err != nil {
				log.Println("Error listening for client:", err)
				continue
			}

			if _, ok := host.iters[client.Name]; !ok {
				host.iters[client.Name] = make(map[string]*gtk.TreeIter)
			}

			glib.IdleAdd(func() {
				for _, roomName := range client.Rooms {
					host.AddRoomRow(client.Name, roomName)
				}

			})

			if _, ok := host.peers[client.Name]; ok {
				// peer already found
				continue
			}

			conn, err := net.Dial("tcp", client.Addr)
			if err != nil {
				log.Println("Error connecting to peer:", err)
				continue
			}

			host.peers[client.Name] = conn
		}
	}()

	// Broadcast host
	go func() {
		for {
			time.Sleep(time.Second)

			hostClient.Rooms = hostClient.Rooms[:0]
			for k := range host.Server.Rooms {
				hostClient.Rooms = append(hostClient.Rooms, k)
			}

			buf, err := json.Marshal(hostClient)
			if err != nil {
				log.Fatal(err)
			}

			err = roomTopic.Publish(host.ctx, buf)
			if err != nil {
				log.Println("Error sending info", err)
				continue
			}
		}
	}()
}
