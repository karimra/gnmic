package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karimra/gnmic/collector"
)

func (c *Config) sanitizeSubscription() {
	allEmpty := true
	for i := range c.SubscribePath {
		c.SubscribePath[i] = strings.Trim(c.SubscribePath[i], "[]")
	}
	for i := range c.SubscribePath {
		allEmpty = c.SubscribePath[i] == ""
		if !allEmpty {
			return
		}
	}
	if allEmpty {
		c.SubscribePath = []string{}
	}
}
func (c *Config) GetSubscriptions() (map[string]*collector.SubscriptionConfig, error) {
	subscriptions := make(map[string]*collector.SubscriptionConfig)

	if len(c.SubscribePath) > 0 && len(c.SubscribeName) > 0 {
		return nil, fmt.Errorf("flags --path and --name cannot be mixed")
	}
	if len(c.SubscribePath) > 0 {
		sub := new(collector.SubscriptionConfig)
		//sub.Name = fmt.Sprintf("default-%d", time.Now().Unix())
		sub.Paths = c.SubscribePath
		sub.Prefix = c.SubscribePrefix
		sub.Target = c.SubscribeTarget
		sub.Mode = c.SubscribeMode
		sub.Encoding = c.Encoding
		sub.Qos = c.SubscribeQos
		sub.StreamMode = c.SubscribeStreamMode
		sub.HeartbeatInterval = c.SubscribeHeartbearInterval
		sub.SampleInterval = c.SubscribeSampleInteral
		sub.SuppressRedundant = c.SubscribeSuppressRedundant
		sub.UpdatesOnly = c.SubscribeUpdatesOnly
		sub.Models = c.SubscribeModel
		subscriptions["default"] = sub
		if c.Debug {
			c.logger.Printf("subscriptions: %+v", subscriptions)
		}
		return subscriptions, nil
	}

	for sn, sub := range c.Subscriptions {
		sub.Name = sn
		// inherit global "subscribe-*" option if it's not set
		c.setSubscriptionDefaults(sub)
	}
	if len(c.SubscribeName) == 0 {
		if c.Debug {
			c.logger.Printf("subscriptions: %+v", c.Subscriptions)
		}
		return c.Subscriptions, nil
	}
	filteredSubscriptions := make(map[string]*collector.SubscriptionConfig)
	notFound := make([]string, 0)
	for _, name := range c.SubscribeName {
		if s, ok := c.Subscriptions[name]; ok {
			filteredSubscriptions[name] = s
		} else {
			notFound = append(notFound, name)
		}
	}
	if len(notFound) > 0 {
		return nil, fmt.Errorf("named subscription(s) not found in config file: %v", notFound)
	}
	if c.Debug {
		c.logger.Printf("subscriptions: %+v", subscriptions)
	}
	return filteredSubscriptions, nil
}

func (c *Config) setSubscriptionDefaults(sub *collector.SubscriptionConfig) {
	if sub.SampleInterval == nil {
		sub.SampleInterval = c.SubscribeSampleInteral
	}
	if sub.HeartbeatInterval == nil {
		sub.HeartbeatInterval = c.SubscribeHeartbearInterval
	}
	if sub.Encoding == "" {
		sub.Encoding = c.Encoding
	}
	if sub.Mode == "" {
		sub.Mode = c.SubscribeMode
	}
	if strings.ToUpper(sub.Mode) == "STREAM" && sub.StreamMode == "" {
		sub.StreamMode = c.SubscribeStreamMode
	}
	if sub.Qos == nil {
		sub.Qos = c.SubscribeQos
	}
}

func (c *Config) GetSubscriptionsList() []*collector.SubscriptionConfig {
	subs, err := c.GetSubscriptions()
	if err != nil {
		c.logger.Printf("failed to get subscriptions: %v", err)
		return nil
	}
	subsList := make([]*collector.SubscriptionConfig, 0, len(subs))
	for _, sub := range subs {
		subsList = append(subsList, sub)
	}
	sort.Slice(subsList, func(i, j int) bool {
		return subsList[i].Name < subsList[j].Name
	})
	return subsList
}
