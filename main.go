package main

import (
	"chalkboard/canvas"
	"chalkboard/client"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"time"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

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

	host, err := client.NewPeer(name, node, ctx, ps)
	if err != nil {
		log.Fatal(err)
	}

	s := mdns.NewMdnsService(node, "chalkboard", host)
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

func initWindow(win *gtk.Window, host *client.Peer) {
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

	drawArea, err := BuilderGetObject[*gtk.DrawingArea](builder, "draw")
	if err != nil {
		log.Fatal(err)
	}

	if err != nil {
		log.Fatal(err)
	}

	var currentRoom *canvas.Canvas

	roomView.SetModel(host.Rooms)
	roomView.Connect("row-activated",
		func(tree *gtk.TreeView, path *gtk.TreePath, column *gtk.TreeViewColumn) {
			model := host.Rooms.ToTreeModel()

			iter, err := host.Rooms.GetIter(path)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			ownerName, err := ModelGetValue[string](model, iter, client.OWNER_NAME)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			roomName, err := ModelGetValue[string](model, iter, client.ROOM_NAME)
			if err != nil {
				log.Printf("Error joining room: %s", err)
				return
			}

			if currentRoom != nil && currentRoom.OwnerName != host.Name {
				err = currentRoom.Close()
				if err != nil {
					log.Println("Error closing room", ownerName, roomName, err)
				}
			}

			c, err := host.JoinRoom(ownerName, roomName)
			if err != nil {
				log.Println("Error joining room", ownerName, roomName, err)
				return
			}

			currentRoom = c
			log.Printf("Joined /%s/%s", ownerName, roomName)
		})

	addRoomButton.Connect("clicked", func() {
		name, err := roomName.GetText()
		if err != nil {
			log.Printf("Error adding room: %s", err)
			return
		}

		err = host.Server.CreateRoom(host.Name, name)
		if err != nil {
			log.Printf("Error adding room: %s", err)
			return
		}

		host.AddRoomRow(host.Name, name)
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
		if currentRoom == nil {
			return
		}

		currentRoom.Draw(cr)
	})

	drawArea.Connect("motion-notify-event", func(d *gtk.DrawingArea, event *gdk.Event) {
		b := gdk.EventButtonNewFromEvent(event)
		if b.State()&uint(gdk.BUTTON_PRESS_MASK) == 0 {
			// button not pressed
			buttonPressed = false
			return
		}

		if currentRoom == nil {
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
		currentRoom.Write(host.Name, currentLine)
		drawArea.QueueDraw()
	})
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
