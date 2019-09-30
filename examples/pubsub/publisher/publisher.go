package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"

	"github.com/gammazero/nexus/examples/newclient"
	"github.com/gammazero/nexus/wamp"
)

const exampleTopic = "example.hello"
const exampleCountTopic = "example.eventCount"

func main() {

	newclient.ParseArgs()

	max := 20
	msgCount := 20
	fmt.Printf("PUBLISHER %d clients\n", max)

	wg := sync.WaitGroup{}
	wg.Add(max)

	var success uint64
	var failure uint64

	for ii := 1; ii <= max; ii++ {
		go func(loop int) {

			logger := log.New(os.Stdout, fmt.Sprintf("PUBLISHER %d > ", loop), 0)
			// Connect publisher client with requested socket type and serialization.
			publisher, err := newclient.MyNewClient(logger)
			if err != nil {
				logger.Fatal(err)
			}
			defer publisher.Close()

			// Publish to topic.
			for jj := 1; jj <= msgCount; jj++ {
				args := wamp.List{ fmt.Sprintf("%d:%d", loop, jj, ) }
				err = publisher.Publish(exampleCountTopic, nil, args, nil)
				if err != nil {
					logger.Printf("message %d error: %s\n", jj, err)
					atomic.AddUint64(&failure, 1)
				} else {
					//logger.Printf("message %d: success\n", jj)
					atomic.AddUint64(&success, 1)
				}
			}

			logger.Printf("Sent %d messages to %s\n", msgCount, exampleCountTopic)
			wg.Done()

		}(ii)
	}

	fmt.Printf("Waiting PUBLISHER %d clients\n", max)
	wg.Wait()
	fmt.Printf("Done PUBLISHER %d clients %d success %d failure\n", max, success, failure)

}
