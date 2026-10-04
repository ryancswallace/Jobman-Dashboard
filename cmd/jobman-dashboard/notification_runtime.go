package main

import (
	"errors"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

type notificationRuntime struct {
	policy    *notifications.DevicePolicy
	cipher    *notifications.DeviceCipher
	providers map[notifications.DeviceTopic]*push.APNs
}

func (r *notificationRuntime) Close() {
	if r != nil {
		for _, provider := range r.providers {
			provider.Close()
		}
	}
}

// This loader validates local private material only. It never registers a device
// or contacts Apple; an empty provider set permits inbox/device development
// before an operator supplies approved APNs credentials.
func loadNotificationRuntime(c config.Config, currentKey []byte) (result *notificationRuntime, err error) {
	if len(c.Notifications.DeviceTopics) == 0 {
		return nil, nil
	}
	result = &notificationRuntime{providers: map[notifications.DeviceTopic]*push.APNs{}}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	topics := make([]notifications.DeviceTopic, len(c.Notifications.DeviceTopics))
	for i, topic := range c.Notifications.DeviceTopics {
		topics[i] = notifications.DeviceTopic{Topic: topic.Topic, Environment: topic.Environment}
	}
	result.policy, err = notifications.NewDevicePolicy(topics)
	if err != nil {
		return result, errors.New("notification device topic policy is invalid")
	}
	keys := map[string][]byte{c.Encryption.KeyID: currentKey}
	for _, previous := range c.Notifications.PreviousTokenKeys {
		key, readErr := config.ReadSecret(previous.KeyFile, 32)
		if readErr != nil || len(key) != 32 {
			return result, errors.New("notification token read keys must contain exactly32 private raw bytes")
		}
		if _, exists := keys[previous.KeyID]; exists {
			return result, errors.New("notification token key IDs must be unique")
		}
		keys[previous.KeyID] = key
	}
	result.cipher, err = notifications.NewDeviceCipher(c.Encryption.KeyID, keys)
	if err != nil {
		return result, errors.New("notification token encryption material is invalid")
	}
	for _, provider := range c.Notifications.APNs {
		pair := notifications.DeviceTopic{Topic: provider.Topic, Environment: provider.Environment}
		if !result.policy.Allows(pair.Topic, pair.Environment) || result.providers[pair] != nil || !c.Events.Enabled {
			return result, errors.New("APNs provider does not match the enabled notification configuration")
		}
		key, readErr := config.ReadSecret(provider.PrivateKeyFile, 16384)
		if readErr != nil {
			return result, errors.New("APNs signing key must be a bounded private file")
		}
		client, newErr := push.NewAPNs(push.APNsConfig{Topic: pair.Topic, Environment: pair.Environment, TeamID: provider.TeamID, KeyID: provider.KeyID, PrivateKeyPEM: key})
		if newErr != nil {
			return result, errors.New("APNs signing material or provider identity is invalid")
		}
		result.providers[pair] = client
	}
	return result, nil
}
