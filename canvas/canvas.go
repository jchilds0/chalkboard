package canvas

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"

	"github.com/gotk3/gotk3/cairo"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

type Message struct {
	LineIndex  int
	Line       Line
	SenderID   string
	SenderName string
}

type Canvas struct {
	Topic  *pubsub.Topic
	Active chan bool
	ctx    context.Context
	id     peer.ID

	lineLock sync.Mutex
	lines    map[string]Line
}

func NewCanvas(topic *pubsub.Topic, ctx context.Context, id peer.ID) (*Canvas, error) {
	c := &Canvas{
		Active: make(chan bool, 10),
		Topic:  topic,
		ctx:    ctx,
		id:     id,
		lines:  make(map[string]Line),
	}

	go c.readLoop()
	return c, nil
}

func (c *Canvas) Draw(cr *cairo.Context) {
	c.lineLock.Lock()

	for _, line := range c.lines {
		line.Draw(cr)
	}

	c.lineLock.Unlock()
}

func (c *Canvas) Put(host string, l Line) {
	name := host + "-" + strconv.Itoa(l.Index)

	c.lineLock.Lock()
	c.lines[name] = l
	c.lineLock.Unlock()
}

func (c *Canvas) readLoop() {
	var (
		sub    *pubsub.Subscription
		active bool
		err    error
	)

	for {
		// update sub state
		if active && sub == nil {
			sub, err = c.Topic.Subscribe()
			if err != nil {
				log.Print("Error activating subscription:", err)
			}

			log.Println("Subscribed:", c.Topic.String())
		} else if !active && sub != nil {
			sub.Cancel()
			sub = nil

			log.Println("Closed Subscription:", c.Topic.String())
		}

		// check for close or open subscription
		if active {
			select {
			case newState := <-c.Active:
				active = newState
				continue
			default:
			}
		} else {
			active = <-c.Active
			continue
		}

		msg, err := sub.Next(c.ctx)
		if err != nil {
			sub = nil
			log.Println("Error getting the next message", err)
			continue
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
}
