package main

import (
	"chalkboard/canvas"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

type Room struct {
	Owner string
	Rooms []string
	iters map[string]*gtk.TreeIter
}

type Peer struct {
	name        string
	node        host.Host
	ctx         context.Context
	ps          *pubsub.PubSub
	currentRoom string
	roomLock    sync.Mutex

	roomNames map[string]Room
	rooms     map[string]*canvas.Canvas
}

func main() {
	nameFlag := flag.String("name", "", "Name to use in whiteboard")
	flag.Parse()

	ctx := context.Background()

	// start a libp2p node with default settings
	node, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))

	if err != nil {
		log.Fatal(err)
	}

	ps, err := pubsub.NewGossipSub(ctx, node)
	if err != nil {
		log.Fatal(err)
	}

	name := *nameFlag
	if len(name) == 0 {
		pretty := node.ID().String()
		peer := pretty[len(pretty)-8:]
		name = fmt.Sprintf("%s-%s", os.Getenv("USER"), peer)
	}

	host := Peer{
		name: name,
		node: node,
		ctx:  ctx,
		ps:   ps,

		roomNames: make(map[string]Room, 128),
		rooms:     make(map[string]*canvas.Canvas, 128),
	}

	go host.listenRooms()

	s := mdns.NewMdnsService(node, "chalkboard", &host)
	err = s.Start()
	if err != nil {
		log.Fatal(err)
	}

	gtk.Init(nil)
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal(err)
	}

	win.SetTitle("Chalkboard")
	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	initWindow(win, &host)

	win.SetDecorated(true)
	win.ShowAll()
	gtk.Main()
}

func initWindow(win *gtk.Window, host *Peer) {
	builder, err := gtk.BuilderNewFromFile("./gui.ui")
	if err != nil {
		log.Fatal(err)
	}

	box, err := BuilderGetObject[*gtk.Paned](builder, "body")
	win.Add(box)

	roomView, err := BuilderGetObject[*gtk.TreeView](builder, "rooms")
	if err != nil {
		log.Fatal(err)
	}

	roomName, err := BuilderGetObject[*gtk.Entry](builder, "room-name")
	if err != nil {
		log.Fatal(err)
	}

	addRoomButton, err := BuilderGetObject[*gtk.Button](builder, "add-room")
	if err != nil {
		log.Fatal(err)
	}

	refreshRoomButton, err := BuilderGetObject[*gtk.Button](builder, "refresh-room")
	if err != nil {
		log.Fatal(err)
	}

	drawArea, err := BuilderGetObject[*gtk.DrawingArea](builder, "draw")
	if err != nil {
		log.Fatal(err)
	}

	roomModel, err := gtk.ListStoreNew(glib.TYPE_STRING)
	if err != nil {
		log.Fatal(err)
	}

	roomRefresh := func() {
		host.roomLock.Lock()
		for _, r := range host.roomNames {
			for _, name := range r.Rooms {
				if _, ok := r.iters[name]; ok {
					continue
				}

				r.iters[name] = roomModel.Append()
				roomModel.SetValue(r.iters[name], 0, name)
			}
		}

		host.roomLock.Unlock()
	}

	refreshRoomButton.Connect("clicked", func() {
		go roomRefresh()
	})

	roomView.SetModel(roomModel)
	roomView.Connect("row-activated",
		func(tree *gtk.TreeView, path *gtk.TreePath, column *gtk.TreeViewColumn) {
			iter, err := roomModel.GetIter(path)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			name, err := ModelGetValue[string](roomModel.ToTreeModel(), iter, 0)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			err = host.joinRoom(name)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			log.Printf("Joined %s", name)
		})

	addRoomButton.Connect("clicked", func() {
		name, err := roomName.GetText()
		if err != nil {
			log.Printf("Error adding room: %s", err)
			return
		}

		host.roomLock.Lock()
		var room Room
		if _, ok := host.roomNames[host.name]; !ok {
			room = Room{
				Owner: host.name,
				Rooms: make([]string, 0, 128),
				iters: make(map[string]*gtk.TreeIter, 128),
			}
		} else {
			room = host.roomNames[host.name]
		}

		room.Rooms = append(room.Rooms, name)
		host.roomNames[room.Owner] = room
		host.roomLock.Unlock()

		roomRefresh()
	})

	// messages from other users
	drawArea.AddEvents(gdk.BUTTON1_MASK)
	drawArea.AddEvents(int(gdk.POINTER_MOTION_MASK))

	go func() {
		for {
			time.Sleep(50 * time.Millisecond)
			drawArea.QueueDraw()
		}
	}()

	var currentLine canvas.Line
	buttonPressed := false

	drawArea.Connect("draw", func(d *gtk.DrawingArea, cr *cairo.Context) {
		room, ok := host.rooms[host.currentRoom]
		if !ok {
			return
		}

		room.Draw(cr)
	})

	drawArea.Connect("motion-notify-event", func(d *gtk.DrawingArea, event *gdk.Event) {
		b := gdk.EventButtonNewFromEvent(event)
		if b.State()&uint(gdk.BUTTON_PRESS_MASK) == 0 {
			// button not pressed
			buttonPressed = false
			return
		}

		if _, ok := host.rooms[host.currentRoom]; !ok {
			return
		}

		if !buttonPressed {
			currentLine = canvas.Line{
				Index:  currentLine.Index + 1,
				Red:    0,
				Green:  0,
				Blue:   0,
				Width:  2,
				Points: make([]canvas.Point, 0, 1024),
			}
			buttonPressed = true
		}

		p := canvas.Point{
			X: b.X(),
			Y: b.Y(),
		}

		currentLine.Points = append(currentLine.Points, p)
		host.publish(currentLine, currentLine.Index)
		drawArea.QueueDraw()
	})
}

func (host *Peer) joinRoom(roomName string) error {
	if room, ok := host.rooms[host.currentRoom]; ok {
		room.Active <- false
	}

	if _, ok := host.rooms[roomName]; !ok {
		topic, err := host.ps.Join(roomName)
		if err != nil {
			return err
		}

		room, err := canvas.NewCanvas(topic, host.ctx, host.node.ID())
		if err != nil {
			return err
		}

		host.rooms[roomName] = room
	}

	host.currentRoom = roomName
	host.rooms[host.currentRoom].Active <- true
	return nil
}

func (host *Peer) publish(l canvas.Line, lineIndex int) error {
	peerID := host.node.ID()
	msg := canvas.Message{
		Line:       l,
		LineIndex:  lineIndex,
		SenderID:   peerID.String(),
		SenderName: host.name,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	room, ok := host.rooms[host.currentRoom]
	if !ok {
		return nil
	}

	room.Put(host.name, l)
	return room.Topic.Publish(host.ctx, msgBytes)
}

// HandlePeerFound connects to peers discovered via mDNS. Once they're connected,
// the PubSub system will automatically start interacting with them if they also
// support PubSub.
func (host *Peer) HandlePeerFound(pi peer.AddrInfo) {
	fmt.Printf("discovered new peer %s\n", pi.ID)
	err := host.node.Connect(context.Background(), pi)
	if err != nil {
		fmt.Printf("error connecting to peer %s: %s\n", pi.ID, err)
	}
}

func (host *Peer) listenRooms() {
	roomTopic, err := host.ps.Join("/rooms")
	if err != nil {
		log.Fatal(err)
	}

	roomSub, err := roomTopic.Subscribe()
	if err != nil {
		log.Fatal()
	}

	// Recieve rooms for other hosts
	go func() {
		for {
			msg, err := roomSub.Next(host.ctx)
			if err != nil {
				log.Println("Error listening for rooms:", err)
				continue
			}

			// ignore our own messages
			if msg.ReceivedFrom == host.node.ID() {
				continue
			}

			var rooms Room
			err = json.Unmarshal(msg.Data, &rooms)
			if err != nil {
				log.Println("Error listening for rooms:", err)
				continue
			}

			log.Printf("Received rooms: %v", rooms)

			// update rooms map
			host.roomLock.Lock()
			if _, ok := host.roomNames[rooms.Owner]; ok {
				rooms.iters = host.roomNames[rooms.Owner].iters
			} else {
				rooms.iters = make(map[string]*gtk.TreeIter, 128)
			}

			host.roomNames[rooms.Owner] = rooms
			host.roomLock.Unlock()
		}
	}()

	// Send out rooms for this host
	go func() {
		for {
			time.Sleep(time.Second)

			host.roomLock.Lock()
			data, err := json.Marshal(host.roomNames[host.name])
			host.roomLock.Unlock()
			if err != nil {
				log.Println("Error sending rooms:", err)
				continue
			}

			roomTopic.Publish(host.ctx, data)
		}
	}()
}

func BuilderGetObject[T any](builder *gtk.Builder, name string) (obj T, err error) {
	gtkObject, err := builder.GetObject(name)
	if err != nil {
		return
	}

	goObj, ok := gtkObject.(T)
	if !ok {
		err = fmt.Errorf("Builder object '%s' is type %v", name, reflect.TypeOf(goObj))
		return
	}

	return goObj, nil
}

func ModelGetValue[T any](model *gtk.TreeModel, iter *gtk.TreeIter, col int) (obj T, err error) {
	id, err := model.GetValue(iter, col)
	if err != nil {
		return
	}

	goObj, err := id.GoValue()
	if err != nil {
		return
	}

	obj, ok := goObj.(T)
	if !ok {
		err = fmt.Errorf("Model value in col '%d' is type %v", col, reflect.TypeOf(goObj))
		return
	}

	return
}
