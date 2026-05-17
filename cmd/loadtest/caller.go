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
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
	"github.com/pion/sdp/v3"
)

type Caller struct {
	dialogUA    *sipgo.DialogUA
	dialogSess  *sipgo.DialogClientSession
	sipAddr     string
	callID      string
	codecName   string
	localIP     net.IP
	rtpPort     int
	sampleRate  int
	payloadType uint8
}

func NewCaller(client *sipgo.Client, sipAddr string, localIP net.IP, rtpPort int, payloadType uint8, codecName string, sampleRate int) *Caller {
	contactAddr := sip.Uri{
		User: "loadtest",
		Host: localIP.String(),
		Port: 5060, // Dummy
	}

	dialogUA := &sipgo.DialogUA{
		Client:     client,
		ContactHDR: sip.ContactHeader{Address: contactAddr},
	}

	return &Caller{
		dialogUA:    dialogUA,
		sipAddr:     sipAddr,
		localIP:     localIP,
		rtpPort:     rtpPort,
		callID:      uuid.New().String(),
		payloadType: payloadType,
		codecName:   codecName,
		sampleRate:  sampleRate,
	}
}

func (c *Caller) Dial(ctx context.Context) error {
	offer, err := c.generateSDPOffer()
	if err != nil {
		return fmt.Errorf("generating SDP offer: %w", err)
	}

	host, portStr, err := net.SplitHostPort(c.sipAddr)
	if err != nil {
		host = c.sipAddr
		portStr = "5060"
	}
	port, _ := strconv.Atoi(portStr)

	fromURI := sip.Uri{User: "loadtest", Host: c.localIP.String()}
	toURI := sip.Uri{User: "echo", Host: host, Port: port}

	req := sip.NewRequest(sip.INVITE, toURI)
	req.AppendHeader(sip.NewHeader("From", fmt.Sprintf("<%s>;tag=%s", fromURI.String(), uuid.New().String())))
	req.AppendHeader(sip.NewHeader("To", fmt.Sprintf("<%s>", toURI.String())))
	req.AppendHeader(sip.NewHeader("Call-ID", c.callID))
	req.AppendHeader(sip.NewHeader("CSeq", "1 INVITE"))
	req.SetBody(offer)
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))

	sess, err := c.dialogUA.WriteInvite(ctx, req)
	if err != nil {
		return fmt.Errorf("sending INVITE: %w", err)
	}

	err = sess.WaitAnswer(ctx, sipgo.AnswerOptions{})
	if err != nil {
		return fmt.Errorf("waiting for answer: %w", err)
	}

	// Send ACK
	err = sess.Ack(ctx)
	if err != nil {
		return fmt.Errorf("sending ACK: %w", err)
	}

	c.dialogSess = sess
	return nil
}

func (c *Caller) Hangup(ctx context.Context) error {
	if c.dialogSess == nil {
		return nil
	}
	return c.dialogSess.Bye(ctx)
}

func (c *Caller) generateSDPOffer() ([]byte, error) {
	sessionID := uint64(time.Now().UnixNano())

	desc := sdp.SessionDescription{
		Version: sdp.Version(0),
		Origin: sdp.Origin{
			Username:       "loadtest",
			SessionID:      sessionID,
			SessionVersion: sessionID,
			NetworkType:    "IN",
			AddressType:    "IP4",
			UnicastAddress: c.localIP.String(),
		},
		SessionName: sdp.SessionName("LoadTest Session"),
		ConnectionInformation: &sdp.ConnectionInformation{
			NetworkType: "IN",
			AddressType: "IP4",
			Address:     &sdp.Address{Address: c.localIP.String()},
		},
		TimeDescriptions: []sdp.TimeDescription{
			{Timing: sdp.Timing{StartTime: 0, StopTime: 0}},
		},
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Port:    sdp.RangedPort{Value: c.rtpPort},
					Protos:  []string{"RTP", "AVP"},
					Formats: []string{fmt.Sprint(c.payloadType), "101"},
				},
				Attributes: []sdp.Attribute{
					{Key: "rtpmap", Value: fmt.Sprintf("%d %s/%d", c.payloadType, c.codecName, c.sampleRate)},
					{Key: "rtpmap", Value: fmt.Sprintf("101 telephone-event/%d", c.sampleRate)},
					{Key: "fmtp", Value: "101 0-16"},
					{Key: "ptime", Value: "20"},
					{Key: "sendrecv"},
				},
			},
		},
	}

	return desc.Marshal()
}
