package main

import (
	"chalkboard/canvas"
	"chalkboard/client"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

var currentRoom *canvas.Canvas
var eraser bool
var pencil = canvas.Pencil{
	Width: 10,
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
	screen, err := gdk.ScreenGetDefault()
	if err != nil {
		log.Fatal(err)
	}

	provider, err := gtk.CssProviderNew()
	if err != nil {
		log.Fatal(err)
	}

	err = provider.LoadFromPath("./gui.css")
	if err != nil {
		log.Fatal(err)
	}

	gtk.AddProviderForScreen(screen, provider, 600)

	builder, err := gtk.BuilderNewFromFile("./gui.ui")
	if err != nil {
		log.Fatal(err)
	}

	box, err := BuilderGetObject[*gtk.Paned](builder, "body")
	win.Add(box)

	err = initToolbar(builder)
	if err != nil {
		log.Fatal(err)
	}

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

	drawArea, err := BuilderGetObject[*gtk.DrawingArea](builder, "draw-area")
	if err != nil {
		log.Fatal(err)
	}

	roomView.SetModel(host.Rooms)
	roomView.Connect("row-activated",
		func(tree *gtk.TreeView, path *gtk.TreePath, column *gtk.TreeViewColumn) {
			if currentRoom != nil && currentRoom.OwnerName != host.Name {
				err = currentRoom.Close()
				if err != nil {
					log.Println("Error closing room:", currentRoom.OwnerName, currentRoom.RoomName, err)
				}
			}

			c, err := host.JoinRoomPath(path)
			if err != nil {
				log.Println("Error joining room:", err)
				return
			}

			currentRoom = c
			log.Printf("Joined /%s/%s", c.OwnerName, c.RoomName)
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
	drawArea.Connect("draw", func(d *gtk.DrawingArea, cr *cairo.Context) {
		if currentRoom == nil {
			return
		}

		cr.SetSourceRGB(1, 1, 1)
		cr.Paint()

		currentRoom.Draw(cr)
	})

	buttonPressed := false
	drawArea.Connect("motion-notify-event", func(d *gtk.DrawingArea, event *gdk.Event) {
		b := gdk.EventButtonNewFromEvent(event)
		if b.State()&uint(gdk.BUTTON_PRESS_MASK) == 0 {
			// button not pressed
			buttonPressed = false
			// lastPress = time.Now()
			return
		}

		if currentRoom == nil {
			return
		}

		// log.Println(time.Now().Sub(lastPress))
		// lastPress = time.Now()

		p := canvas.Point{X: b.X(), Y: b.Y()}

		if eraser {
			currentRoom.ErasePoint(p)
		} else {
			currentRoom.AddPoint(pencil, buttonPressed, p)
		}

		buttonPressed = true
	})

	go func() {
		for {
			time.Sleep(20 * time.Millisecond)
			drawArea.QueueDraw()
		}
	}()
}

func initToolbar(builder *gtk.Builder) error {
	fixed, err := BuilderGetObject[*gtk.Fixed](builder, "tool-fixed")
	if err != nil {
		return err
	}

	toolbox, err := BuilderGetObject[*gtk.Box](builder, "tool-box")
	if err != nil {
		return err
	}

	buttonPressed := false
	posX, posY := 0, 0
	delX, delY := 0, 0

	toolbox.AddEvents(gdk.BUTTON1_MASK)
	toolbox.AddEvents(int(gdk.POINTER_MOTION_MASK))
	toolbox.Connect("motion-notify-event", func(box *gtk.Box, event *gdk.Event) {
		b := gdk.EventButtonNewFromEvent(event)
		if b.State()&uint(gdk.BUTTON_PRESS_MASK) == 0 {
			// button not pressed
			buttonPressed = false
			return
		}

		if !buttonPressed {
			xptr, err := fixed.Container.ChildGetProperty(toolbox, "x", glib.TYPE_INT)
			if err != nil {
				log.Println("Error moving toolbox:", err)
				return
			}

			yptr, err := fixed.Container.ChildGetProperty(toolbox, "y", glib.TYPE_INT)
			if err != nil {
				log.Println("Error moving toolbox:", err)
				return
			}

			posX = xptr.(int)
			posY = yptr.(int)

			delX = int(b.XRoot())
			delY = int(b.YRoot())

			buttonPressed = true
			return
		}

		fixed.Move(toolbox, posX+int(b.XRoot())-delX, posY+int(b.YRoot())-delY)
	})

	toolbar, err := BuilderGetObject[*gtk.Toolbar](builder, "toolbar")
	if err != nil {
		return err
	}

	{
		pencilButton, err := BuilderGetObject[*gtk.RadioButton](builder, "pencil-button")
		if err != nil {
			return err
		}

		err = setToolButtonIcon(pencilButton, "assets/edit-pen-icon.svg")
		if err != nil {
			return err
		}

		pencilButton.Connect("clicked", func() {
			eraser = !pencilButton.GetActive()
		})
	}

	{
		eraserButton, err := BuilderGetObject[*gtk.RadioButton](builder, "eraser-button")
		if err != nil {
			return err
		}

		err = setToolButtonIcon(eraserButton, "assets/eraser-icon.svg")
		if err != nil {
			return err
		}

		eraserButton.Connect("clicked", func() {
			eraser = eraserButton.GetActive()
		})
	}

	pencilWidths := []float64{4, 8, 12, 16, 24}
	buttons := make([]*gtk.RadioButton, len(pencilWidths))
	var widthGroup *glib.SList

	for i, width := range pencilWidths {
		buttons[i], err = newToolButtonWithCircle(widthGroup, strconv.FormatFloat(width, 'f', 10, 64), width)
		if err != nil {
			return err
		}

		widthGroup, err = buttons[i].GetGroup()
		if err != nil {
			return err
		}

		item, err := gtk.ToolItemNew()
		if err != nil {
			return err
		}

		item.Add(buttons[i])
		toolbar.Add(item)

		sep, err := gtk.SeparatorToolItemNew()
		if err != nil {
			return err
		}

		sep.SetDraw(false)
		toolbar.Add(sep)
	}

	pencil.Width = pencilWidths[0]

	color, err := gtk.ColorButtonNew()
	if err != nil {
		return err
	}

	color.Connect("color-set", func() {
		rgb := color.GetRGBA()

		pencil.Red = rgb.GetRed()
		pencil.Green = rgb.GetGreen()
		pencil.Blue = rgb.GetBlue()

		toolbar.QueueDraw()
	})

	item, err := gtk.ToolItemNew()
	if err != nil {
		return err
	}

	item.Add(color)
	toolbar.Add(item)

	sep, err := gtk.SeparatorToolItemNew()
	if err != nil {
		return err
	}

	toolbar.Add(sep)

	return nil
}

func newToolButtonWithCircle(group *glib.SList, label string, width float64) (*gtk.RadioButton, error) {
	areaSize := 24

	area, err := gtk.DrawingAreaNew()
	if err != nil {
		return nil, err
	}

	area.SetSizeRequest(areaSize, areaSize)
	area.Connect("draw", func(da *gtk.DrawingArea, cr *cairo.Context) {
		cr.SetSourceRGB(pencil.Red, pencil.Green, pencil.Blue)
		cr.Arc(float64(areaSize)/2, float64(areaSize)/2, width/2, 0, 2*math.Pi)
		cr.Fill()
	})

	button, err := gtk.RadioButtonNew(group)
	if err != nil {
		return nil, err
	}

	button.SetMode(false)
	button.Connect("clicked", func() {
		pencil.Width = width
	})

	button.Add(area)
	button.SetVisible(true)
	button.SetTooltipText(label)

	return button, nil
}

func setToolButtonIcon(button *gtk.RadioButton, fileName string) error {
	buf, err := gdk.PixbufNewFromFileAtSize(fileName, 18, 18)
	if err != nil {
		return err
	}

	img, err := gtk.ImageNewFromPixbuf(buf)
	if err != nil {
		return err
	}

	button.Add(img)
	return nil
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
