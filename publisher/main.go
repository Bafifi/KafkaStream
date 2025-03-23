package main

import (
	"context"
	"encoding/json"
	"fmt"
	"kafkastream/models"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	consumerGroupID   = "Publishers"
	publisherInstance = "publisher"
	outputDir         = "/app/output"
)

func getBrokers() []string {
	brokersEnv := os.Getenv("KAFKA_BROKERS")
	if brokersEnv == "" {
		return []string{"kafka1:9092"}
	}
	return strings.Split(brokersEnv, ",")
}

// waitForTopic checks if a topic exists and waits until it does or timeout occurs
func waitForTopic(brokers []string, topic string, maxSeconds int) {
	log.Printf("Checking if topic %s exists...", topic)

	// Choose the first broker for the check
	brokerAddress := brokers[0]

	for i := 0; i < maxSeconds; i++ {
		// Create a temporary connection to list topics
		conn, err := kafka.Dial("tcp", brokerAddress)
		if err != nil {
			log.Printf("Cannot connect to Kafka (attempt %d/%d): %v", i+1, maxSeconds, err)
			time.Sleep(1 * time.Second)
			continue
		}

		partitions, err := conn.ReadPartitions(topic)
		conn.Close()

		if err == nil && len(partitions) > 0 {
			log.Printf("Topic %s found with %d partitions", topic, len(partitions))
			return
		}

		if err != nil {
			log.Printf("Topic %s not ready yet (attempt %d/%d): %v",
				topic, i+1, maxSeconds, err)
		} else {
			log.Printf("Topic %s has 0 partitions (attempt %d/%d)",
				topic, i+1, maxSeconds)
		}

		time.Sleep(1 * time.Second)
	}

	log.Printf("Warning: Topic %s may not exist after waiting %d seconds", topic, maxSeconds)
}

func main() {
	// Get instance number from env var or use default
	instanceNum := os.Getenv("INSTANCE")
	if instanceNum == "" {
		instanceNum = "1"
	}
	instanceName := fmt.Sprintf("%s-%s", publisherInstance, instanceNum)

	// By default, we'll listen to the one transformed topic we expect
	transformedTopic := "transformed-Producer-S3-TestType"

	log.Printf("[%s] Starting publisher watching topic: %s in consumer group: %s",
		instanceName, transformedTopic, consumerGroupID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down publisher...")
		cancel()
	}()

	// Get brokers from environment
	brokers := getBrokers()
	log.Printf("Using Kafka brokers: %v", brokers)

	// Wait for Kafka to be ready and topic to be created
	log.Println("Waiting for Kafka to be ready...")
	waitForTopic(brokers, transformedTopic, 30)

	// Create Kafka reader with more diagnostic options
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       transformedTopic,
		GroupID:     consumerGroupID,
		MinBytes:    10e3, // 10KB
		MaxBytes:    10e6, // 10MB
		StartOffset: kafka.FirstOffset,
		MaxWait:     time.Second,
		Logger:      kafka.LoggerFunc(log.Printf),
		ErrorLogger: kafka.LoggerFunc(log.Printf),
	})
	defer reader.Close()

	log.Printf("Publisher %s started in consumer group: %s reading from topic: %s",
		instanceName, consumerGroupID, transformedTopic)

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}
	log.Printf("Output directory verified: %s", outputDir)

	// Create output file
	outputFile := filepath.Join(outputDir, fmt.Sprintf("output-%s.json", instanceName))
	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open output file: %v", err)
	}
	defer f.Close()
	log.Printf("Output file opened: %s", outputFile)

	log.Printf("[%s] Waiting for messages on topic: %s", instanceName, transformedTopic)

	// Process messages
	for {
		select {
		case <-ctx.Done():
			return
		default:
			log.Printf("[%s] Attempting to read message from topic: %s", instanceName, transformedTopic)
			m, err := reader.ReadMessage(ctx)
			if err != nil {
				if strings.Contains(err.Error(), "context canceled") {
					log.Printf("[%s] Context canceled", instanceName)
					return
				}
				log.Printf("[%s] Error reading message: %v", instanceName, err)
				time.Sleep(5 * time.Second) // Add delay to prevent CPU spinning
				continue
			}

			startTime := time.Now()

			log.Printf("[%s] Received message from partition: %d offset: %d",
				instanceName, m.Partition, m.Offset)

			// Parse the event
			var event models.Event
			if err := json.Unmarshal(m.Value, &event); err != nil {
				log.Printf("[%s] Error unmarshaling event: %v, raw data: %s",
					instanceName, err, string(m.Value))
				continue
			}

			log.Printf("[%s] Publishing event: %s from partition: %d offset: %d (took: %v)",
				instanceName, event.ID, m.Partition, m.Offset, time.Since(startTime))

			// Add received timestamp
			outputEvent := struct {
				models.Event
				ReceivedAt time.Time `json:"receivedAt"`
				Publisher  string    `json:"publisher"`
			}{
				Event:      event,
				ReceivedAt: time.Now(),
				Publisher:  instanceName,
			}

			// Write to file
			outputJSON, err := json.MarshalIndent(outputEvent, "", "  ")
			if err != nil {
				log.Printf("[%s] Error marshaling output event: %v", instanceName, err)
				continue
			}

			if _, err := f.WriteString(string(outputJSON) + "\n"); err != nil {
				log.Printf("[%s] Error writing to output file: %v", instanceName, err)
			} else {
				log.Printf("[%s] Wrote event: %s to file (total processing time: %v)",
					instanceName, event.ID, time.Since(startTime))
			}

			// Flush to ensure data is written to disk
			f.Sync()
		}
	}
}
