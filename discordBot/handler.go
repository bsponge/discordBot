package discordBot

import "strings"

func (client *DiscordClient) handleDispatch(m map[string]interface{}) {
	t := m["t"].(string)
	switch t {
	case "READY":
		client.UserId = m["d"].(map[string]interface{})["user"].(map[string]interface{})["id"].(string)
		client.SessionId = m["d"].(map[string]interface{})["session_id"].(string)
		client.muS.Lock()
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
		client.muS.Unlock()
	case "GUILD_CREATE":
		client.ServerId = m["d"].(map[string]interface{})["id"].(string)
		client.updateServerInfo(m)
		client.muS.Lock()
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
		client.muS.Unlock()
	case "VOICE_SERVER_UPDATE":
		client.VoiceEndpoint = m["d"].(map[string]interface{})["endpoint"].(string)
		client.Token = m["d"].(map[string]interface{})["token"].(string)
		client.muS.Lock()
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
		client.muS.Unlock()
		client.ConnectToVoiceEndpoint()
	case "VOICE_STATE_UPDATE":
		client.SessionId = m["d"].(map[string]interface{})["session_id"].(string)
		client.muS.Lock()
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
		client.muS.Unlock()
		voiceStateUpdateData := m["d"].(map[string]interface{})
		userId := voiceStateUpdateData["user_id"].(string)
		voiceChannelId := voiceStateUpdateData["channel_id"]
		if voiceChannelId == nil {
			delete(client.members, userId)
		} else {
			memberToUpdate, ok := client.members[userId]
			if ok {
				memberToUpdate.voiceChannelId = voiceChannelId.(string)
			} else {
				client.members[userId] = &member{User: user{Id: userId}, voiceChannelId: voiceChannelId.(string)}
			}
		}
	case "MESSAGE_CREATE":
		msg := m["d"].(map[string]interface{})["content"].(string)
		action := strings.Split(msg, " ")

		switch action[0] {
		case "play":
			if len(action) > 1 {
				go client.listenForMusicRequests()
				userId := m["d"].(map[string]interface{})["author"].(map[string]interface{})["id"].(string)
				if _, ok := client.members[userId]; ok {
					voiceChannelId := client.members[userId].voiceChannelId
					client.musicQueue <- action[1:]
					if client.VoiceConn == nil {
						client.ConnectToVoiceChannel(voiceChannelId)
						client.finishedPlaying <- true
					}
				}
			}
		case "skip":
			client.QuitAudioCh <- true
		}
	}
}

func (client *DiscordClient) handleHello(m map[string]interface{}) {
	client.HeartBeatInterval = int(m["d"].(map[string]interface{})["heartbeat_interval"].(float64))
	client.identify()
	go client.sendHeartBeatEvery(client.HeartBeatInterval)
	client.muS.Lock()
	if m["s"] == nil {
		client.s = 0
	} else {
		client.s = int(m["s"].(float64))
	}
	client.muS.Unlock()
}

func (client *DiscordClient) handleHeartbeatAck(m map[string]interface{}) {
	client.muS.Lock()
	if m["s"] == nil {
		client.s = 0
	} else {
		client.s = int(m["s"].(float64))
	}
	client.muS.Unlock()
}