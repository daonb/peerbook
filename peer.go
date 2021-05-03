// Copyright 2021 TUZIG LTD and peerbook Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 6 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = 5 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 4096
	DefaultHomeUrl = "https://pb.terminal7.dev"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  maxMessageSize,
	WriteBufferSize: maxMessageSize,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Peer is a middleman between the websocket connection and the hub.
type Peer struct {
	FP          string `redis:"fp" json:"fp"`
	Name        string `redis:"name" json:"name,omitempty"`
	User        string `redis:"user" json:"user,omitempty"`
	Kind        string `redis:"kind" json:"kind,omitempty"`
	Verified    bool   `redis:"verified" json:"verified,omitempty"`
	CreatedOn   int64  `redis:"created_on" json:"created_on,omitempty"`
	VerifiedOn  int64  `redis:"verified_on" json:"verified_on,omitempty"`
	LastConnect int64  `redis:"last_connect" json:"last_connect,omitempty"`
	Online      bool   `redis:"online" json:"online"`
}
type PeerList []*Peer

// StatusMessage is used to update the peer to a change of state,
// like 200 after the peer has been authorized
type StatusMessage struct {
	Code int    `json:"code"`
	Text string `json:"text"`
}

// OfferMessage is the format of the offer message after processing -
// including the source_name & source_fp read from the db
type OfferMessage struct {
	SourceName string `json:"source_name"`
	SourceFP   string `json:"source_fp"`
	Offer      string `json:"offer"`
}

// AnswerMessage is the format of the answer message after processing -
// including the source_name & source_fp read from the db
type AnswerMessage struct {
	SourceName string `json:"source_name"`
	SourceFP   string `json:"source_fp"`
	Answer     string `json:"answer"`
}

// Getting the list of users peers
func GetUsersPeers(email string) (*PeerList, error) {
	var l PeerList
	u, err := db.GetUser(email)
	if err != nil {
		return nil, err
	}
	// TODO: use redis transaction to read them all at once
	for _, fp := range *u {
		p, err := GetPeer(fp)
		if err != nil {
			Logger.Warnf("Failed to read peer: %w", err)
			if err != nil {
				Logger.Errorf("Failed to send status message: %s", err)
			}
		} else {
			l = append(l, p)
		}
	}
	return &l, nil
}

func (p *Peer) setName(name string) {
	p.Name = name
	conn := db.pool.Get()
	defer conn.Close()
	conn.Do("HSET", p.Key(), "name", name)
}

// SetOnline sets the peer's online redis cache and notifies peers
func (p *Peer) SetOnline(o bool) {
	temp := Conn{FP: p.FP, User: p.User}
	temp.SetOnline(o)
}
func (p *Peer) Key() string {
	return fmt.Sprintf("peer:%s", p.FP)
}
func NewPeer(fp string, name string, user string, kind string) *Peer {
	return &Peer{FP: fp, Name: name, Kind: kind, CreatedOn: time.Now().Unix(),
		User: user, Verified: false, Online: false}
}
