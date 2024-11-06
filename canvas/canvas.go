package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/gotk3/gotk3/cairo"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

type Message struct {
	Line       Line
	SenderID   string
	SenderName string
}

type Canvas struct {
	OwnerName string
	RoomName  string
	active    bool
	topic     *pubsub.Topic
	sub       *pubsub.Subscription
	ctx       context.Context
	cancel    context.CancelFunc
	id        peer.ID

	lineLock sync.Mutex
	Lines    map[string]Line
}

var currentLine Line

func NewCanvas(owner, room string, topic *pubsub.Topic, ctx context.Context, id peer.ID) (*Canvas, error) {
	c := &Canvas{
		OwnerName: owner,
		RoomName:  room,
		Lines:     make(map[string]Line, 1024),
	}

	err := c.InitCanvas(topic, ctx, id)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Canvas) AddPoint(pencil Pencil, pressed bool, x, y float64) {
	if !pressed {
		currentLine = Line{
			Index:  currentLine.Index + 1,
			Pencil: pencil,
			Points: make([]Point, 0, 1024),
		}
	}

	p := Point{X: x, Y: y}

	currentLine.Points = append(currentLine.Points, p)
	c.Write(c.OwnerName, currentLine)
}

func add(p1, p2 Point) Point {
	return Point{
		X: p1.X + p2.X,
		Y: p1.Y + p2.Y,
	}
}

func dot(p1, p2 Point) float64 {
	return p1.X*p2.X + p1.Y*p2.Y
}

func norm2(p Point) float64 {
	return dot(p, p)

}

func scale(p Point, k float64) Point {
	return Point{
		X: k * p.X,
		Y: k * p.Y,
	}
}

func (l Line) intersects(p Point) bool {
	if len(l.Points) <= 1 {
		return false
	}

	for i := range l.Points[1:] {
		p1 := l.Points[i]
		p2 := l.Points[i+1]

		a := add(p, scale(p1, -1))
		b := add(p2, scale(p1, -1))

		s := dot(a, b) / dot(b, b)
		dist2 := norm2(add(a, scale(b, -s)))

		if dot(a, b) < 0 || dot(a, b) > norm2(b) {
			continue
		}

		if dist2 > l.Pencil.Width {
			continue
		}

		return true
	}

	return false
}

func (c *Canvas) DrawErasor(cr *cairo.Context) {
	name := c.OwnerName + "-1"
	l := c.Lines[name]

	cr.SetSourceRGB(1, 0, 0)
	for x := range 1920 {
		for y := range 1080 {
			if l.intersects(Point{X: float64(x), Y: float64(y)}) {
				continue
			}

			cr.Rectangle(float64(x), float64(y), 1, 1)
		}
	}

	cr.Fill()
}

func (c *Canvas) ErasePoint(x, y float64) {
	p := Point{X: x, Y: y}

	for s, l := range c.Lines {
		if !l.intersects(p) {
			continue
		}

		l.Points = l.Points[:0]
		c.Lines[s] = l
	}
}

func (c *Canvas) Write(senderName string, l Line) error {
	msg := Message{
		Line:       l,
		SenderID:   c.id.String(),
		SenderName: senderName,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.Put(senderName, l)
	if c.topic == nil {
		return fmt.Errorf("Missing topic for room %s", c.RoomName)
	}

	return c.topic.Publish(c.ctx, msgBytes)
}

func (c *Canvas) Draw(cr *cairo.Context) {
	c.lineLock.Lock()

	for _, line := range c.Lines {
		line.Draw(cr)
	}

	c.lineLock.Unlock()
}

func (c *Canvas) Put(host string, l Line) {
	name := host + "-" + strconv.Itoa(l.Index)

	c.lineLock.Lock()
	c.Lines[name] = l
	c.lineLock.Unlock()
}

func (c *Canvas) Close() error {
	log.Println("Closing Room", c.RoomName)
	c.cancel()

	if c.sub != nil {
		c.sub.Cancel()
	}

	err := c.topic.Close()
	return err
}

func (c *Canvas) InitCanvas(topic *pubsub.Topic, ctx context.Context, id peer.ID) error {
	c.topic = topic
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.id = id

	if c.sub != nil {
		return fmt.Errorf("Subscription already created")
	}

	var err error
	c.sub, err = c.topic.Subscribe()
	if err != nil {
		return fmt.Errorf("Error activating subscription: %s", err)
	}

	go func() {
		defer func() {
			c.sub.Cancel()
			log.Println("Closed Subscription:", c.topic.String())
		}()

		for {
			msg, err := c.sub.Next(c.ctx)
			if err != nil {
				return
			}

			if msg.ReceivedFrom == c.id {
				continue
			}

			cm := new(Message)
			err = json.Unmarshal(msg.Data, cm)
			if err != nil {
				continue
			}

			c.Put(cm.SenderName, cm.Line)
		}
	}()

	return nil
}
