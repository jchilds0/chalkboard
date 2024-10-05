package main

import (
	"chalkboard/utils"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"

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
	name      string
	node      host.Host
	ctx       context.Context
	ps        *pubsub.PubSub
	roomTopic *pubsub.Topic
	topics    map[string]*pubsub.Topic
	rooms     map[string]*room
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

	host := Peer{
		name:   name,
		node:   node,
		ctx:    ctx,
		ps:     ps,
		topics: make(map[string]*pubsub.Topic, 128),
		rooms:  make(map[string]*room, 128),
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

	initWindow(win, host)

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

func initWindow(win *gtk.Window, host Peer) {
	builder, err := gtk.BuilderNewFromFile("./gui.ui")
	if err != nil {
		log.Fatal(err)
	}

	box, err := utils.BuilderGetObject[*gtk.Paned](builder, "body")
	win.Add(box)

	var room *room
	var l line
	buttonPressed := false

	roomView, err := utils.BuilderGetObject[*gtk.TreeView](builder, "rooms")
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

			if _, ok := host.rooms[name]; ok {
				room = host.rooms[name]
				return
			}

			room, err = joinRoom(host, name)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			log.Printf("Joined %s", name)
		})

	roomName, err := utils.BuilderGetObject[*gtk.Entry](builder, "room-name")
	if err != nil {
		log.Fatal(err)
	}

	addRoomButton, err := utils.BuilderGetObject[*gtk.Button](builder, "add-room")
	if err != nil {
		log.Fatal(err)
	}

	addRoomButton.Connect("clicked", func() {
		name, err := roomName.GetText()
		if err != nil {
			log.Printf("Error adding room: %s", err)
			return
		}

		// create topic for room
		topic, err := host.ps.Join(name)
		if err != nil {
			log.Printf("Error adding room: %s", err)
			return
		}

		host.topics[name] = topic

		iter := roomModel.Append()
		roomModel.SetValue(iter, 0, name)
	})

	drawArea, err := utils.BuilderGetObject[*gtk.DrawingArea](builder, "draw")
	if err != nil {
		log.Fatal(err)
	}

	// messages from other users
	drawArea.AddEvents(gdk.BUTTON1_MASK)
	drawArea.AddEvents(int(gdk.POINTER_MOTION_MASK))

	go func() {
		// receive new line
		for {
			if room == nil {
				continue
			}

			m := <-room.Messages
			room.lineLock.Lock()

			if _, ok := room.lines[m.SenderID]; !ok {
				room.lines[m.SenderID] = make(map[int]line, 1024)
			}

			room.lines[m.SenderID][m.LineIndex] = m.Line
			room.lineCount = max(m.LineIndex, room.lineCount)

			room.lineLock.Unlock()
			drawArea.QueueDraw()
		}
	}()

	drawArea.Connect("draw", func(d *gtk.DrawingArea, cr *cairo.Context) {
		if room == nil {
			return
		}

		room.lineLock.Lock()

		for _, lines := range room.lines {
			for _, line := range lines {
				line.draw(cr)
			}
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

		if room == nil {
			return
		}

		if !buttonPressed {
			l = line{
				Index:  l.Index + 1,
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

		l.Points = append(l.Points, p)
		room.publish(l, l.Index)
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
	host     Peer
	topic    *pubsub.Topic
	sub      *pubsub.Subscription

	roomName  string
	lines     map[string]map[int]line
	lineLock  sync.Mutex
	lineCount int
}

func joinRoom(host Peer, roomName string) (*room, error) {
	topic, ok := host.topics[roomName]
	if !ok {
		err := fmt.Errorf("Missing topic %s", roomName)
		return nil, err
	}

	sub, err := topic.Subscribe()
	if err != nil {
		return nil, err
	}

	r := &room{
		host:      host,
		topic:     topic,
		sub:       sub,
		roomName:  roomName,
		lineCount: 0,

		lines:    make(map[string]map[int]line, 128),
		Messages: make(chan *message, 10),
	}

	go r.readLoop()
	host.rooms[roomName] = r
	return r, nil
}

func (r *room) publish(l line, lineIndex int) error {
	peerID := r.host.node.ID()
	msg := message{
		Line:       l,
		LineIndex:  lineIndex,
		SenderID:   peerID.String(),
		SenderName: r.host.name,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return r.topic.Publish(r.host.ctx, msgBytes)
}

func (r *room) readLoop() {
	for {
		msg, err := r.sub.Next(r.host.ctx)
		if err != nil {
			close(r.Messages)
			return
		}

		cm := new(message)
		err = json.Unmarshal(msg.Data, cm)
		if err != nil {
			continue
		}

		// send valid messages onto the Messages channel
		r.Messages <- cm
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
