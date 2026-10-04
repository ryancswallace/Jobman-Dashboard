package config

import (
	"errors"
	"regexp"
)

type NotificationTopic struct {
	Topic       string `json:"topic"`
	Environment string `json:"environment"`
}
type APNsProvider struct {
	Topic          string `json:"topic"`
	Environment    string `json:"environment"`
	TeamID         string `json:"teamId"`
	KeyID          string `json:"keyId"`
	PrivateKeyFile string `json:"privateKeyFile"`
}
type Notifications struct {
	DeviceTopics      []NotificationTopic `json:"deviceTopics,omitempty"`
	PreviousTokenKeys []Encryption        `json:"previousTokenKeys,omitempty"`
	APNs              []APNsProvider      `json:"apns,omitempty"`
}

var notificationTopic = regexp.MustCompile(`^[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$`)
var appleIdentifier = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func validateNotifications(c Notifications, current Encryption, events Events) error {
	if len(c.DeviceTopics) > 16 || len(c.APNs) > 16 || len(c.PreviousTokenKeys) > 7 {
		return errors.New("notification provider/key/topic configuration exceeds its bounds")
	}
	allowed := map[NotificationTopic]bool{}
	for _, topic := range c.DeviceTopics {
		if len(topic.Topic) > 255 || !notificationTopic.MatchString(topic.Topic) || topic.Environment != "sandbox" && topic.Environment != "production" || allowed[topic] {
			return errors.New("notification device topic/environment is invalid or duplicated")
		}
		allowed[topic] = true
	}
	keys := map[string]bool{current.KeyID: true}
	for _, key := range c.PreviousTokenKeys {
		if !text(key.KeyID, 64) || !absolutePath(key.KeyFile) || keys[key.KeyID] {
			return errors.New("previous notification token keys require unique IDs and private absolute file paths")
		}
		keys[key.KeyID] = true
	}
	providers := map[NotificationTopic]bool{}
	for _, provider := range c.APNs {
		topic := NotificationTopic{provider.Topic, provider.Environment}
		if !allowed[topic] || providers[topic] || !appleIdentifier.MatchString(provider.TeamID) || !appleIdentifier.MatchString(provider.KeyID) || !absolutePath(provider.PrivateKeyFile) {
			return errors.New("APNs providers require distinct allowed topics/environments, Apple IDs and private key paths")
		}
		providers[topic] = true
	}
	if len(c.APNs) > 0 && !events.Enabled {
		return errors.New("APNs providers require events.enabled=true")
	}
	if len(c.PreviousTokenKeys) > 0 && len(c.DeviceTopics) == 0 {
		return errors.New("previous notification token keys require configured device topics")
	}
	return nil
}
