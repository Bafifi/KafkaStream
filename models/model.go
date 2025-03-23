package models

import (
	"fmt"
	"time"
)

// Event represents a message with metadata and payload
type Event struct {
	ID           string      `json:"id"`
	Creator      string      `json:"creator"`
	Sink         string      `json:"sink"`
	ResourceType string      `json:"resourceType"`
	ResourceID   string      `json:"resourceId"`
	Version      string      `json:"version"`
	Timestamp    time.Time   `json:"timestamp"`
	Payload      interface{} `json:"payload"`
}

// GetTransformedTopic returns the transformed topic name based on event metadata
func (e *Event) GetTransformedTopic() string {
	return fmt.Sprintf("transformed-%s-%s-%s", e.Creator, e.Sink, e.ResourceType)
}
