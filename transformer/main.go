package main

import (
	"context"
	"encoding/json"
	"fmt"
	"kafkastream/models"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	sourceTopic         = "ingest-events"
	consumerGroupID     = "Transformers"
	brokerAddress       = "kafka:9092"
	transformerInstance = "transformer"
)

func main() {
	// Get instance number from env var or use default
	instanceNum := os.Getenv("INSTANCE")
	if instanceNum == "" {
		instanceNum = "1"
	}
	instanceName := fmt.Sprintf("%s-%s", transformerInstance, instanceNum)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down transformer...")
		cancel()
	}()

	// Wait for Kafka to be ready
	log.Println("Waiting for Kafka to be ready...")
	time.Sleep(20 * time.Second)

	// Create Kafka reader
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{brokerAddress},
		Topic:       sourceTopic,
		GroupID:     consumerGroupID,
		MinBytes:    10e3, // 10KB
		MaxBytes:    10e6, // 10MB
		StartOffset: kafka.FirstOffset,
		MaxWait:     time.Second,
	})
	defer reader.Close()

	log.Printf("Transformer %s started in consumer group: %s reading from topic: %s",
		instanceName, consumerGroupID, sourceTopic)

	// Process messages
	for {
		select {
		case <-ctx.Done():
			return
		default:
			m, err := reader.ReadMessage(ctx)
			if err != nil {
				if !strings.Contains(err.Error(), "context canceled") {
					log.Printf("Error reading message: %v", err)
				}
				continue
			}

			startTime := time.Now()

			// Parse the event
			var event models.Event
			if err := json.Unmarshal(m.Value, &event); err != nil {
				log.Printf("Error unmarshaling event: %v", err)
				continue
			}

			log.Printf("[%s] Processing event: %s from partition: %d offset: %d (took: %v)",
				instanceName, event.ID, m.Partition, m.Offset, time.Since(startTime))

			// Get the transformed topic name
			transformedTopic := event.GetTransformedTopic()

			// Create a writer for the transformed topic
			writer := kafka.NewWriter(kafka.WriterConfig{
				Brokers:  []string{brokerAddress},
				Topic:    transformedTopic,
				Balancer: &kafka.Hash{},
			})

			// Forward the event to the transformed topic
			err = writer.WriteMessages(ctx, kafka.Message{
				Key:     m.Key,
				Value:   m.Value,
				Headers: m.Headers,
			})
			writer.Close()

			if err != nil {
				log.Printf("Error writing to transformed topic: %v", err)
			} else {
				log.Printf("[%s] Forwarded event: %s to topic: %s (total processing time: %v)",
					instanceName, event.ID, transformedTopic, time.Since(startTime))
			}
		}
	}
}
