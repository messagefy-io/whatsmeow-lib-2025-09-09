// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package events contains all the events that whatsmeow.Client emits to functions registered with AddEventHandler.
package events

import (
	"fmt"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

// QR is emitted after connecting when there's no session data in the device store.
//
// The QR codes are available in the Codes slice. You should render the strings as QR codes one by
// one, switching to the next one whenever the duration specified in the Timeout field has passed.
//
// When the QR code has been scanned and pairing is complete, PairSuccess will be emitted. If you
// run out of codes before scanning, the server will close the websocket, and you will have to
// reconnect to get more codes.
type QR struct {
	Codes   []string
	Timeout time.Duration
}

// PairSuccess is emitted after the QR code has been scanned with the phone and the handshake has
// been completed. Note that this is generally followed by a websocket reconnection, so you should
// wait for the Connected before trying to send anything.
type PairSuccess struct {
	ID           types.JID
	BusinessName string
	Platform     string
}

// PairError is emitted when a pair-success event is received from the server, but finishing the pairing locally fails.
type PairError struct {
	ID           types.JID
	BusinessName string
	Platform     string
	Error        error
}

// QRScannedWithoutMultidevice is emitted when the pairing QR code is scanned, but the phone didn't have multidevice enabled.
// The same QR code can still be scanned after this event, which means the user can just be told to enable multidevice and re-scan the code.
type QRScannedWithoutMultidevice struct{}

// Connected is emitted when the client has successfully connected to the WhatsApp servers
// and is authenticated. The user who the client is authenticated as will be in the device store
// at this point, which is why this event doesn't contain any data.
type Connected struct{}

// LoggedOut is emitted when the client has been unpaired from the phone.
//
// This can happen while connected (stream:error messages) or right after connecting (connect failure messages).
type LoggedOut struct {
	// OnConnect is true if the event was triggered by a connect failure message.
	// If it's false, the event was triggered by a stream:error message.
	OnConnect bool
}

// ConnectFailure is emitted when the WhatsApp server sends a <failure> node with an unknown reason.
//
// Known reasons are handled internally and emitted as different events (e.g. LoggedOut).
type ConnectFailure struct {
	Reason string
	Raw    *waBinary.Node
}

// StreamError is emitted when the WhatsApp server sends a <stream:error> node with an unknown code.
//
// Known codes are handled internally and emitted as different events (e.g. LoggedOut).
type StreamError struct {
	Code string
	Raw  *waBinary.Node
}

// Disconnected is emitted when the websocket is closed by the server.
type Disconnected struct{}

// HistorySync is emitted when the phone has sent a blob of historical messages.
type HistorySync struct {
	Data *waProto.HistorySync
}

// UndecryptableMessage is emitted when receiving a new message that failed to decrypt.
//
// The library will automatically ask the sender to retry. If the sender resends the message,
// and it's decryptable, then it will be emitted as a normal Message event.
//
// The UndecryptableMessage event may also be repeated if the resent message is also undecryptable.
type UndecryptableMessage struct {
	Info types.MessageInfo

	// IsUnavailable is true if the recipient device didn't send a ciphertext to this device at all
	// (as opposed to sending a ciphertext, but the ciphertext not being decryptable).
	IsUnavailable bool
}

// Message is emitted when receiving a new message.
type Message struct {
	Info        types.MessageInfo // Information about the message like the chat and sender IDs
	Message     *waProto.Message  // The actual message struct
	IsEphemeral bool
	IsViewOnce  bool

	// The raw message struct. This is the raw unwrapped data, which means the actual message might
	// be wrapped in DeviceSentMessage, EphemeralMessage or ViewOnceMessage.
	RawMessage *waProto.Message
}

// ReceiptType represents the type of a Receipt event.
type ReceiptType string

const (
	// ReceiptTypeDelivered means the message was delivered to the device (but the user might not have noticed).
	ReceiptTypeDelivered ReceiptType = ""
	// ReceiptTypeRead means the user opened the chat and saw the message.
	ReceiptTypeRead ReceiptType = "read"
)

// GoString returns the name of the Go constant for the ReceiptType value.
func (rt ReceiptType) GoString() string {
	switch rt {
	case ReceiptTypeRead:
		return "events.ReceiptTypeRead"
	case ReceiptTypeDelivered:
		return "events.ReceiptTypeDelivered"
	default:
		return fmt.Sprintf("events.ReceiptType(%#v)", string(rt))
	}
}

// Receipt is emitted when an outgoing message is delivered to or read by another user, or when another device reads an incoming message.
type Receipt struct {
	types.MessageSource
	MessageID   string
	Timestamp   time.Time
	Type        ReceiptType
	PreviousIDs []string // Additional message IDs that were read. Only present for read receipts.
}

// ChatPresence is emitted when a chat state update (also known as typing notification) is received.
type ChatPresence struct {
	types.MessageSource
	State types.ChatPresence
}

// GroupInfo is emitted when the metadata of a group changes.
type GroupInfo struct {
	JID       types.JID  // The group ID in question
	Notify    string     // Seems like a top-level type for the invite
	Sender    *types.JID // The user who made the change. Doesn't seem to be present when notify=invite
	Timestamp time.Time  // The time when the change occurred

	Name     *types.GroupName     // Group name change
	Topic    *types.GroupTopic    // Group topic (description) change
	Locked   *types.GroupLocked   // Group locked status change (can only admins edit group info?)
	Announce *types.GroupAnnounce // Group announce status change (can only admins send messages?)

	PrevParticipantVersionID string
	ParticipantVersionID     string

	JoinReason string // This will be "invite" if the user joined via invite link

	Join  []types.JID // Users who joined or were added the group
	Leave []types.JID // Users who left or were removed from the group

	Promote []types.JID // Users who were promoted to admins
	Demote  []types.JID // Users who were demoted to normal users

	UnknownChanges []*waBinary.Node
}

// Picture is emitted when a user's profile picture or group's photo is changed.
//
// You can use Client.GetProfilePictureInfo to get the actual image URL after this event.
type Picture struct {
	JID       types.JID // The user or group ID where the picture was changed.
	Author    types.JID // The user who changed the picture.
	Timestamp time.Time // The timestamp when the picture was changed.
	Remove    bool      // True if the picture was removed.
	PictureID string    // The new picture ID if it was not removed.
}
