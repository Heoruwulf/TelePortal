/*
TelePortal: High-performance, zero-allocation bi-directional audio bridge.
Copyright (C) 2026 Mark Horila

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/
package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/Heoruwulf/TelePortal/pkg/api"
	"github.com/redis/go-redis/v9"
)

type RedisWatcher struct {
	client *redis.Client
	events chan api.CallEvent
}

func NewRedisWatcher(addr string) *RedisWatcher {
	rdb := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	return &RedisWatcher{
		client: rdb,
		events: make(chan api.CallEvent, 1000),
	}
}

func (rw *RedisWatcher) Start(ctx context.Context) error {
	pubsub := rw.client.Subscribe(ctx, api.RedisChannelCallEvents)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-ch:
			var event api.CallEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.Printf("Failed to unmarshal redis event: %v", err)
				continue
			}
			select {
			case rw.events <- event:
			default:
				// Dropping event if channel full
			}
		}
	}
}

func (rw *RedisWatcher) Events() <-chan api.CallEvent {
	return rw.events
}

func (rw *RedisWatcher) Close() error {
	return rw.client.Close()
}
