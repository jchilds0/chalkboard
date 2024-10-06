package main

import (
	"chalkboard/utils"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
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

type Peer struct {
	name        string
	node        host.Host
	ctx         context.Context
	ps          *pubsub.PubSub
	roomSub     *pubsub.Subscription
	roomTopic   *pubsub.Topic
	currentRoom string
	roomLock    sync.Mutex

	rooms map[string]*room
}

const roomTopic = "rooms"

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

	topic, err := ps.Join(roomTopic)
	if err != nil {
		log.Fatal(err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		log.Fatal()
	}

	host := Peer{
		name:      name,
		node:      node,
		ctx:       ctx,
		ps:        ps,
		roomSub:   sub,
		roomTopic: topic,

		rooms: make(map[string]*room, 128),
	}

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

type line struct {
	Index  int
	Red    float64
	Green  float64
	Blue   float64
	Width  float64
	Points []point
}

type point struct {
	X float64
	Y float64
}

func initWindow(win *gtk.Window, host *Peer) {
	builder, err := gtk.BuilderNewFromFile("./gui.ui")
	if err != nil {
		log.Fatal(err)
	}

	box, err := utils.BuilderGetObject[*gtk.Paned](builder, "body")
	win.Add(box)

	var currentLine line
	buttonPressed := false

	roomView, err := utils.BuilderGetObject[*gtk.TreeView](builder, "rooms")
	if err != nil {
		log.Fatal(err)
	}

	roomName, err := utils.BuilderGetObject[*gtk.Entry](builder, "room-name")
	if err != nil {
		log.Fatal(err)
	}

	addRoomButton, err := utils.BuilderGetObject[*gtk.Button](builder, "add-room")
	if err != nil {
		log.Fatal(err)
	}

	drawArea, err := utils.BuilderGetObject[*gtk.DrawingArea](builder, "draw")
	if err != nil {
		log.Fatal(err)
	}

	roomModel, err := gtk.ListStoreNew(glib.TYPE_STRING)
	if err != nil {
		log.Fatal(err)
	}

	roomView.SetModel(roomModel)
	roomView.Connect("row-activated",
		func(tree *gtk.TreeView, path *gtk.TreePath, column *gtk.TreeViewColumn) {
			iter, err := roomModel.GetIter(path)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			name, err := utils.ModelGetValue[string](roomModel.ToTreeModel(), iter, 0)
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

		iter := roomModel.Append()
		roomModel.SetValue(iter, 0, name)
		drawArea.QueueDraw()
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

	drawArea.Connect("draw", func(d *gtk.DrawingArea, cr *cairo.Context) {
		room := host.getCurrentRoom()
		if room == nil {
			return
		}

		room.lineLock.Lock()

		for _, line := range room.lines {
			line.draw(cr)
		}

		room.lineLock.Unlock()
	})

	drawArea.Connect("motion-notify-event", func(d *gtk.DrawingArea, event *gdk.Event) {
		b := gdk.EventButtonNewFromEvent(event)
		if b.State()&uint(gdk.BUTTON_PRESS_MASK) == 0 {
			// button not pressed
			buttonPressed = false
			return
		}

		room := host.getCurrentRoom()
		if room == nil {
			return
		}

		if !buttonPressed {
			currentLine = line{
				Index:  currentLine.Index + 1,
				Red:    0,
				Green:  0,
				Blue:   0,
				Width:  2,
				Points: make([]point, 0, 1024),
			}
			buttonPressed = true
		}

		p := point{
			X: b.X(),
			Y: b.Y(),
		}

		currentLine.Points = append(currentLine.Points, p)
		host.publish(currentLine, currentLine.Index)
		drawArea.QueueDraw()
	})
}

func (l *line) draw(cr *cairo.Context) {
	if len(l.Points) == 0 {
		return
	}

	cr.SetSourceRGB(l.Red, l.Green, l.Blue)

	start := l.Points[0]
	cr.MoveTo(start.X, start.Y)
	for _, p := range l.Points {
		cr.LineTo(p.X, p.Y)
	}

	cr.SetLineWidth(l.Width)
	cr.Stroke()
}

type message struct {
	LineIndex  int
	Line       line
	SenderID   string
	SenderName string
}

type room struct {
	Messages chan *message
	active   chan bool
	host     *Peer
	topic    *pubsub.Topic

	roomName string
	lineLock sync.Mutex
	lines    map[string]line
}

func newRoom(host *Peer, name string) (*room, error) {
	topic, err := host.ps.Join(name)
	if err != nil {
		return nil, err
	}

	r := &room{
		Messages: make(chan *message, 10),
		active:   make(chan bool, 10),
		host:     host,
		topic:    topic,

		roomName: name,
		lines:    make(map[string]line),
	}

	go r.readLoop()
	return r, nil
}

func (r *room) put(host string, l line) {
	name := host + "-" + strconv.Itoa(l.Index)

	r.lineLock.Lock()
	r.lines[name] = l
	r.lineLock.Unlock()
}

func (host *Peer) joinRoom(roomName string) error {
	if _, ok := host.rooms[host.currentRoom]; ok {
		//room.active <- false
	}

	host.roomLock.Lock()
	if _, ok := host.rooms[roomName]; !ok {
		room, err := newRoom(host, roomName)
		if err != nil {
			host.roomLock.Unlock()
			return err
		}

		host.rooms[roomName] = room
	}

	host.currentRoom = roomName
	host.roomLock.Unlock()

	//host.rooms[host.currentRoom].active <- true
	return nil
}

func (host *Peer) getCurrentRoom() *room {
	host.roomLock.Lock()
	room := host.rooms[host.currentRoom]
	host.roomLock.Unlock()

	return room
}

func (host *Peer) publish(l line, lineIndex int) error {
	peerID := host.node.ID()
	msg := message{
		Line:       l,
		LineIndex:  lineIndex,
		SenderID:   peerID.String(),
		SenderName: host.name,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	room := host.getCurrentRoom()
	if room == nil {
		return nil
	}

	room.put(host.name, l)
	return room.topic.Publish(host.ctx, msgBytes)
}

func (r *room) readLoop() {
	var (
	// active bool
	// err    error
	)

	sub, err := r.topic.Subscribe()
	if err != nil {
		log.Print("Error activating subscription:", err)
	}
	for {
		/*
			// update sub state
			if active && sub == nil {
				sub, err = r.topic.Subscribe()
				if err != nil {
					log.Print("Error activating subscription:", err)
				}
			} else if !active && sub != nil {
				sub.Cancel()
				sub = nil
			}

			// check for close or open subscription
			if active {
				select {
				case newState := <-r.active:
					active = newState
					continue
				default:
				}
			} else {
				active = <-r.active
				continue
			}
		*/

		msg, err := sub.Next(r.host.ctx)
		if err != nil {
			sub = nil
			log.Println("Error getting the next message", err)
			continue
		}

		if msg.ReceivedFrom == r.host.node.ID() {
			continue
		}

		cm := new(message)
		err = json.Unmarshal(msg.Data, cm)
		if err != nil {
			continue
		}

		r.put(cm.SenderName, cm.Line)
	}
}

// HandlePeerFound connects to peers discovered via mDNS. Once they're connected,
// the PubSub system will automatically start interacting with them if they also
// support PubSub.
func (n *Peer) HandlePeerFound(pi peer.AddrInfo) {
	fmt.Printf("discovered new peer %s\n", pi.ID)
	err := n.node.Connect(context.Background(), pi)
	if err != nil {
		fmt.Printf("error connecting to peer %s: %s\n", pi.ID, err)
	}
}
