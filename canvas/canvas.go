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
