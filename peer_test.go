package main

import (
	"chalkboard/canvas"
	"chalkboard/client"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

var numHosts = 24
var names = make([]string, 0, numHosts)
var hosts = make(map[string]*client.Peer, numHosts)
var line = canvas.Line{
	Points: []canvas.Point{
		{X: 0, Y: 0},
		{X: 1, Y: 1},
		{X: 2, Y: 2},
		{X: 3, Y: 3},
	},
}

func TestPeer(t *testing.T) {
	for range numHosts {
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

		pretty := node.ID().String()
		peer := pretty[len(pretty)-8:]
		name := fmt.Sprintf("%s-%s", os.Getenv("USER"), peer)

		host, err := client.NewPeer(name, node, ctx, ps)
		if err != nil {
			log.Fatal(err)
		}

		s := mdns.NewMdnsService(node, "chalkboard", host)
		err = s.Start()
		if err != nil {
			log.Fatal(err)
		}

		log.Println("Started node", name)
		hosts[name] = host
		names = append(names, name)
	}

	// create initial rooms
	for _, h := range hosts {
		for i := range 10 {
			err := h.Server.CreateRoom(h.Name, "test-"+strconv.Itoa(i))
			if err != nil {
				t.Fatalf("[%s] %s", h.Name, err)
			}
		}
	}

	time.Sleep(2 * time.Second)
	var wg sync.WaitGroup

	// write to rooms
	for _, h := range hosts {
		wg.Add(1)
		go simulateHost(h, &wg, line)
	}

	wg.Wait()
}

func simulateHost(host *client.Peer, wg *sync.WaitGroup, line canvas.Line) {
	defer wg.Done()

	for range 30 {
		wait := time.Duration(rand.Int() % 1000)
		time.Sleep(wait * time.Millisecond)

		i := rand.Int() % numHosts
		name := names[i]
		if name == host.Name {
			continue
		}

		j := rand.Int() % 10
		c, err := host.JoinRoom(name, "test-"+strconv.Itoa(j))
		if err != nil {
			log.Fatalf("[%s] %s", host.Name, err)
			return
		}

		line.Index++
		c.Put(host.Name, line)
		log.Printf("[%s] Put line %d to %s %s", host.Name, line.Index, name, c.RoomName)
		c.Close()
	}
}
