package main

import (
	"fmt"
	"github.com/gammazero/nexus/examples/newclient"
	"github.com/gammazero/nexus/wamp"
	"log"
	"os"
	"os/signal"
	"sync"
)

const exampleTopic = "example.hello"
const exampleCountTopic = "example.eventCount"

func main() {
	logger := log.New(os.Stdout, "SUBSCRIBER> ", 0)
	// Connect subscriber client with requested socket type and serialization.
	subscriber, err := newclient.NewClient(logger)
	if err != nil {
		logger.Fatal(err)
	}
	defer subscriber.Close()

	// Define function to handle events received.
	evtHandler := func(args wamp.List, kwargs wamp.Dict, details wamp.Dict) {
		logger.Println("Received", exampleTopic, "event")
		if len(args) != 0 {
			logger.Println("  Event Message:", args[0])
		}
	}

	// Subscribe to topic.
	err = subscriber.Subscribe(exampleTopic, evtHandler, nil)
	if err != nil {
		logger.Fatal("subscribe error:", err)
	}
	logger.Println("Subscribed to", exampleTopic)


	// Define function to handle events count received.
	mapCounts := sync.Map{}
	evtCountHandler := func(args wamp.List, kwargs wamp.Dict, details wamp.Dict) {
		var client, msgCount int
		received, ok := (args[0]).(string)
		if !ok {
			logger.Printf("Event format unexpected: %s", args[0])
			return
		}
		count, err := fmt.Sscanf(received, "%d:%d",&client, &msgCount)
		if err != nil || count != 2 {
			logger.Printf("Event format error %s count %d\n", err, count)
			return
		}
		value, found := mapCounts.Load(client)
		expected := 1
		if found {
			expected = value.(int) + 1
		}
		if msgCount != expected {
			logger.Printf("Client %d Error %d expected %d\n", client, msgCount, expected)
			return
		}
		mapCounts.Store(client, msgCount)
		logger.Printf("Client %d Success %d \n", client, msgCount)

	}



	err = subscriber.Subscribe(exampleCountTopic, evtCountHandler, nil)
	if err != nil {
		logger.Fatal("subscribe error:", err)
	}
	logger.Println("Subscribed to", exampleCountTopic)


	// Wait for CTRL-c or client close while handling events.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
	case <-subscriber.Done():
		logger.Print("Router gone, exiting")
		return // router gone, just exit
	}

	// Unsubscribe from topic.
	if err = subscriber.Unsubscribe(exampleTopic); err != nil {
		logger.Fatal("Failed to unsubscribe:", err)
	}
}
