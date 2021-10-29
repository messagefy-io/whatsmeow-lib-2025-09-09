// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	waBinary "go.mau.fi/whatsmeow/binary"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// IsOnWhatsAppResponse contains information received in response to checking if a phone number is on WhatsApp.
type IsOnWhatsAppResponse struct {
	Query string    // The query string used
	JID   types.JID // The canonical user ID
	IsIn  bool      // Whether the phone is registered or not.

	VerifiedName *types.VerifiedName // If the phone is a business, the verified business details.
}

// IsOnWhatsApp checks if the given phone numbers are registered on WhatsApp.
// The phone numbers should be in international format, including the `+` prefix.
func (cli *Client) IsOnWhatsApp(phones []string) ([]IsOnWhatsAppResponse, error) {
	jids := make([]types.JID, len(phones))
	for i := range jids {
		jids[i] = types.NewJID(phones[i], types.LegacyUserServer)
	}
	list, err := cli.usync(jids, "query", "interactive", []waBinary.Node{
		{Tag: "business", Content: []waBinary.Node{{Tag: "verified_name"}}},
		{Tag: "contact"},
	})
	if err != nil {
		return nil, err
	}
	output := make([]IsOnWhatsAppResponse, 0, len(jids))
	querySuffix := "@" + types.LegacyUserServer
	for _, child := range list.GetChildren() {
		jid, jidOK := child.Attrs["jid"].(types.JID)
		if child.Tag != "user" || !jidOK {
			continue
		}
		var info IsOnWhatsAppResponse
		info.JID = jid
		info.VerifiedName, err = parseVerifiedName(child.GetChildByTag("business"))
		if err != nil {
			cli.Log.Warnf("Failed to parse %s's verified name details: %v", jid, err)
		}
		contactNode := child.GetChildByTag("contact")
		info.IsIn = contactNode.AttrGetter().String("type") == "in"
		contactQuery, _ := contactNode.Content.([]byte)
		info.Query = strings.TrimSuffix(string(contactQuery), querySuffix)
		output = append(output, info)
	}
	return output, nil
}

// GetUserInfo gets basic user info (avatar, status, verified business name, device list).
func (cli *Client) GetUserInfo(jids []types.JID) (map[types.JID]types.UserInfo, error) {
	list, err := cli.usync(jids, "full", "background", []waBinary.Node{
		{Tag: "business", Content: []waBinary.Node{{Tag: "verified_name"}}},
		{Tag: "status"},
		{Tag: "picture"},
		{Tag: "devices", Attrs: waBinary.Attrs{"version": "2"}},
	})
	if err != nil {
		return nil, err
	}
	respData := make(map[types.JID]types.UserInfo, len(jids))
	for _, child := range list.GetChildren() {
		jid, jidOK := child.Attrs["jid"].(types.JID)
		if child.Tag != "user" || !jidOK {
			continue
		}
		verifiedName, err := parseVerifiedName(child.GetChildByTag("business"))
		if err != nil {
			cli.Log.Warnf("Failed to parse %s's verified name details: %v", jid, err)
		}
		status, _ := child.GetChildByTag("status").Content.([]byte)
		pictureID, _ := child.GetChildByTag("picture").Attrs["id"].(string)
		devices := parseDeviceList(jid.User, child.GetChildByTag("devices"), nil, nil)
		respData[jid] = types.UserInfo{
			VerifiedName: verifiedName,
			Status:       string(status),
			PictureID:    pictureID,
			Devices:      devices,
		}
		if verifiedName != nil {
			cli.updateBusinessName(jid, verifiedName.Details.GetVerifiedName())
		}
	}
	return respData, nil
}

// GetUserDevices gets the list of devices that the given user has. The input should be a list of
// regular JIDs, and the output will be a list of AD JIDs. The local device will not be included in
// the output even if the user's JID is included in the input. All other devices will be included.
func (cli *Client) GetUserDevices(jids []types.JID) ([]types.JID, error) {
	list, err := cli.usync(jids, "query", "message", []waBinary.Node{
		{Tag: "devices", Attrs: waBinary.Attrs{"version": "2"}},
	})
	if err != nil {
		return nil, err
	}

	var devices []types.JID
	for _, user := range list.GetChildren() {
		jid, jidOK := user.Attrs["jid"].(types.JID)
		if user.Tag != "user" || !jidOK {
			continue
		}
		parseDeviceList(jid.User, user.GetChildByTag("devices"), &devices, cli.Store.ID)
	}

	return devices, nil
}

// GetProfilePictureInfo gets the URL where you can download a WhatsApp user's profile picture or group's photo.
func (cli *Client) GetProfilePictureInfo(jid types.JID, preview bool) (*types.ProfilePictureInfo, error) {
	attrs := waBinary.Attrs{
		"query": "url",
	}
	if preview {
		attrs["type"] = "preview"
	} else {
		attrs["type"] = "image"
	}
	resp, err := cli.sendIQ(infoQuery{
		Namespace: "w:profile:picture",
		Type:      "get",
		To:        jid,
		Content: []waBinary.Node{{
			Tag:   "picture",
			Attrs: attrs,
		}},
	})
	if err != nil {
		if errors.Is(err, ErrIQError) {
			code := resp.GetChildByTag("error").Attrs["code"].(string)
			if code == "404" {
				return nil, nil
			} else if code == "401" {
				return nil, ErrProfilePictureUnauthorized
			}
		}
		return nil, err
	}
	picture, ok := resp.GetOptionalChildByTag("picture")
	if !ok {
		return nil, fmt.Errorf("missing <picture> element in response to profile picture query")
	}
	var info types.ProfilePictureInfo
	ag := picture.AttrGetter()
	info.ID = ag.String("id")
	info.URL = ag.String("url")
	info.Type = ag.String("type")
	info.DirectPath = ag.String("direct_path")
	if !ag.OK() {
		return &info, ag.Error()
	}
	return &info, nil
}

func (cli *Client) handleHistoricalPushNames(names []*waProto.Pushname) {
	if cli.Store.Contacts == nil {
		return
	}
	for _, user := range names {
		var changed bool
		if jid, err := types.ParseJID(user.GetId()); err != nil {
			cli.Log.Warnf("Failed to parse user ID '%s' in push name history sync: %v", user.GetId(), err)
		} else if changed, _, err = cli.Store.Contacts.PutPushName(jid, user.GetPushname()); err != nil {
			cli.Log.Warnf("Failed to store push name of %s from history sync: %v", err)
		} else if changed {
			cli.Log.Debugf("Got push name %s for %s in history sync", user.GetPushname(), jid)
		}
	}
}

func (cli *Client) handleHistoricalRecent(conversations []*waProto.Conversation) {
	if cli.Store.Groups == nil {
		return
	}
	for _, conversation := range conversations {
		//store groupinfo
		if jid, err := types.ParseJID(conversation.GetId()); err != nil {
			cli.Log.Warnf("Failed to parse user ID '%s' in conversation history sync: %v", conversation.GetId(), err)
		} else if jid.Server == types.GroupServer {
			groupInfo, err := cli.GetGroupInfo(jid)
			if err != nil {
				cli.Log.Warnf("Failed to get group info: %v", err)
			} else {
				err := cli.Store.Groups.PutGroup(*groupInfo)
				if err != nil {
					cli.Log.Warnf("Failed to store group: %v", err)
				}
			}
		}
	}
}

func (cli *Client) updatePushName(user types.JID, messageInfo *types.MessageInfo, name string) {
	if cli.Store.Contacts == nil {
		return
	}
	user = user.ToNonAD()
	changed, previousName, err := cli.Store.Contacts.PutPushName(user, name)
	if err != nil {
		cli.Log.Errorf("Failed to save push name of %s in device store: %v", user, err)
	} else if changed {
		cli.Log.Debugf("Push name of %s changed from %s to %s, dispatching event", user, previousName, name)
		cli.dispatchEvent(&events.PushName{
			JID:         user,
			Message:     messageInfo,
			OldPushName: previousName,
			NewPushName: name,
		})
	}
}

func (cli *Client) updateBusinessName(user types.JID, name string) {
	if cli.Store.Contacts == nil {
		return
	}
	err := cli.Store.Contacts.PutBusinessName(user, name)
	if err != nil {
		cli.Log.Errorf("Failed to save business name of %s in device store: %v", user, err)
	}
}

func parseVerifiedName(businessNode waBinary.Node) (*types.VerifiedName, error) {
	if businessNode.Tag != "business" {
		return nil, nil
	}
	verifiedNameNode, ok := businessNode.GetOptionalChildByTag("verified_name")
	if !ok {
		return nil, nil
	}
	rawCert, ok := verifiedNameNode.Content.([]byte)
	if !ok {
		return nil, nil
	}

	var cert waProto.VerifiedNameCertificate
	err := proto.Unmarshal(rawCert, &cert)
	if err != nil {
		return nil, err
	}
	var certDetails waProto.VerifiedNameDetails
	err = proto.Unmarshal(cert.GetDetails(), &certDetails)
	if err != nil {
		return nil, err
	}
	return &types.VerifiedName{
		Certificate: &cert,
		Details:     &certDetails,
	}, nil
}

func parseDeviceList(user string, deviceNode waBinary.Node, appendTo *[]types.JID, ignore *types.JID) []types.JID {
	deviceList := deviceNode.GetChildByTag("device-list")
	if deviceNode.Tag != "devices" || deviceList.Tag != "device-list" {
		return nil
	}
	children := deviceList.GetChildren()
	if appendTo == nil {
		arr := make([]types.JID, 0, len(children))
		appendTo = &arr
	}
	for _, device := range children {
		deviceID, ok := device.AttrGetter().GetInt64("id", true)
		if device.Tag != "device" || !ok {
			continue
		}
		deviceJID := types.NewADJID(user, 0, byte(deviceID))
		if ignore == nil || deviceJID != *ignore {
			*appendTo = append(*appendTo, deviceJID)
		}
	}
	return *appendTo
}

func (cli *Client) usync(jids []types.JID, mode, context string, query []waBinary.Node) (*waBinary.Node, error) {
	userList := make([]waBinary.Node, len(jids))
	for i, jid := range jids {
		userList[i].Tag = "user"
		if jid.AD {
			jid.AD = false
		}
		switch jid.Server {
		case types.LegacyUserServer:
			userList[i].Content = []waBinary.Node{{
				Tag:     "contact",
				Content: jid.String(),
			}}
		case types.DefaultUserServer:
			userList[i].Attrs = waBinary.Attrs{"jid": jid}
		default:
			return nil, fmt.Errorf("unknown user server '%s'", jid.Server)
		}
	}
	resp, err := cli.sendIQ(infoQuery{
		Namespace: "usync",
		Type:      "get",
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag: "usync",
			Attrs: waBinary.Attrs{
				"sid":     cli.generateRequestID(),
				"mode":    mode,
				"last":    "true",
				"index":   "0",
				"context": context,
			},
			Content: []waBinary.Node{
				{Tag: "query", Content: query},
				{Tag: "list", Content: userList},
			},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send usync query: %w", err)
	} else if list, ok := resp.GetOptionalChildByTag("usync", "list"); !ok {
		return nil, fmt.Errorf("missing usync list element in response to usync query")
	} else {
		return &list, err
	}
}
