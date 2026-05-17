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
	"errors"
	"fmt"
	"log"

	"github.com/Heoruwulf/TelePortal/pkg/audio"
	"github.com/Heoruwulf/TelePortal/pkg/client"
	"golang.org/x/sync/errgroup"
)

type WSEchoAgent struct {
	client *client.Client
	url    string
}

func NewWSEchoAgent(url string) *WSEchoAgent {
	return &WSEchoAgent{
		url: url,
	}
}

func (a *WSEchoAgent) Connect(ctx context.Context) error {
	cfg := client.Config{
		URL: a.url,
		// No ListenOnly because we want to echo what we receive
	}
	c, err := client.NewClient(ctx, cfg)
	if err != nil {
		return err
	}
	a.client = c

	// Echo back any received DTMF
	a.client.OnDTMF(client.DTMFHandler(func(digit string, duration int) {
		if err := a.client.SendDTMF(context.Background(), digit, duration); err != nil {
			log.Printf("Failed to echo DTMF: %v", err)
		}
	}))

	return nil
}

func (a *WSEchoAgent) StartEcho(ctx context.Context) error {
	if a.client == nil {
		return fmt.Errorf("client not connected")
	}

	g, ctx := errgroup.WithContext(ctx)

	// Read and echo loop
	g.Go(func() error {
		for {
			data, err := a.client.Read(ctx)
			if err != nil {
				// We expect context cancellation on shutdown
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return nil
				}
				// The server might have closed the connection normally
				log.Printf("WS read error (or server closed): %v", err)
				return nil
			}

			// Echo the data back
			err = a.client.Write(ctx, data)

			// We MUST return the read buffer to the pool to prevent memory leaks
			audio.PutBuffer(data)

			if err != nil {
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return nil
				}
				log.Printf("WS write error: %v", err)
				return nil
			}
		}
	})

	// Wait for context to close the client
	g.Go(func() error {
		<-ctx.Done()
		return a.client.Close()
	})

	return g.Wait()
}
