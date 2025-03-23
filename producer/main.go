package main

import (
	"context"
	"encoding/json"
	"fmt"
	"kafkastream/models"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
)

const (
	topicName  = "ingest-events"
	numWorkers = 3
	batchSize  = 10
)

func generateRandomID() string {
	return fmt.Sprintf("%d", rand.Intn(1000000))
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down producer...")
		cancel()
	}()

	// Wait for Kafka to be ready
	log.Println("Waiting for Kafka to be ready...")
	time.Sleep(20 * time.Second)

	// Create Kafka writer
	w := &kafka.Writer{
		Addr:                   kafka.TCP("kafka:9092"),
		Topic:                  topicName,
		Balancer:               &kafka.Hash{},
		WriteTimeout:           10 * time.Second,
		RequiredAcks:           kafka.RequireOne,
		AllowAutoTopicCreation: true,
	}
	defer w.Close()

	log.Println("Producer started. Sending events to topic:", topicName)

	// Continuously produce events
	var counter int
	for {
		select {
		case <-ctx.Done():
			return
		default:
			counter++
			event := models.Event{
				ID:           fmt.Sprintf("event-%d", counter),
				Creator:      "Producer",
				Sink:         "S3",
				ResourceType: "TestType",
				ResourceID:   generateRandomID(),
				Version:      "1.0",
				Timestamp:    time.Now(),
				Payload:      fmt.Sprintf("Sample payload %d", counter),
			}

			eventJSON, err := json.Marshal(event)
			if err != nil {
				log.Printf("Error marshaling event: %v", err)
				continue
			}

			// Use resource ID as the key for consistent partition assignment
			err = w.WriteMessages(ctx, kafka.Message{
				Key:   []byte(event.ResourceID),
				Value: eventJSON,
				Headers: []protocol.Header{
					{Key: "creator", Value: []byte(event.Creator)},
					{Key: "sink", Value: []byte(event.Sink)},
					{Key: "resourceType", Value: []byte(event.ResourceType)},
				},
			})

			if err != nil {
				log.Printf("Error writing message: %v", err)
			} else {
				log.Printf("Produced event: %s to topic: %s", event.ID, topicName)
			}

			// Sleep to control production rate
			time.Sleep(500 * time.Millisecond)
		}
	}
}
