package bot

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bsponge/discordBot/pkg/config"
	"github.com/gorilla/websocket"
)

type DiscordVoiceClient struct {
	ctx context.Context

	client *DiscordClient
	cfg    *config.Config

	voiceConn *websocket.Conn

	UserID         string
	voiceEndpoint  string
	voiceToken     string
	voiceSessionID string
	secretKey      [32]byte

	voiceHeartBeatInterval int

	musicQueue     []string
	isPlayingMusic bool

	members map[string]*member

	sequence int

	mtx sync.Mutex
}

type member struct {
	User           user   `json:"user"`
	Mute           bool   `json:"mute"`
	Deaf           bool   `json:"deaf"`
	Roles          []any  `json:"roles"`
	JoinedAt       string `json:"joined_at"`
	HoisedRole     any    `json:"hoisted_role"`
	voiceChannelID string
}

func NewDiscordVoiceClient(cfg *config.Config, client *DiscordClient) (*DiscordVoiceClient, error) {
	return &DiscordVoiceClient{
		cfg:     cfg,
		client:  client,
		members: make(map[string]*member),
	}, nil
}

func (c *DiscordVoiceClient) Start(ctx context.Context, userID string) error {
	c.ctx = ctx

	if c.voiceConn != nil {
		return nil
	}

	member, ok := c.members[userID]
	if !ok {
		return fmt.Errorf("could not find user")
	}

	channelID := member.voiceChannelID

	httpclient := &http.Client{}
	req, err := http.NewRequest("GET", ApiURL+"/users/@me/guilds", nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bot "+c.cfg.Token)
	req = req.WithContext(c.ctx)

	res, err := httpclient.Do(req)
	if err != nil {
		log.Printf("Could not connect to voice channel %s: %s", channelID, err)
		return err
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(res.Body)
	if err != nil {
		return err
	}

	newStr := buf.String()
	newStr = newStr[1 : len(newStr)-1]
	var result map[string]any
	err = json.Unmarshal([]byte(newStr), &result)
	if err != nil {
		return err
	}

	guildID := result["id"].(string)

	js := payload{4, voiceStruct{guildID, channelID, false, false}}
	err = c.client.SendMessage(js)
	if err != nil {
		return err
	}

	return nil
}

func (c *DiscordVoiceClient) QueueRequest(request string) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.musicQueue = append(c.musicQueue, request)
}

type voiceStruct struct {
	GuildID   string `json:"guild_id"`
	ChannelId string `json:"channel_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

func (c *DiscordVoiceClient) HandleServerUpdateDispatch(msg map[string]any) error {
	c.voiceEndpoint = msg["d"].(map[string]any)["endpoint"].(string)
	c.voiceToken = msg["d"].(map[string]any)["token"].(string)

	if msg["s"] == nil {
		c.sequence = 0
	} else {
		c.sequence = int(msg["s"].(float64))
	}

	err := c.connectToVoiceEndpoint()
	if err != nil {
		return err
	}

	return nil
}

func (c *DiscordVoiceClient) HandleStateUpdateDispatch(msg map[string]any) error {
	c.voiceSessionID = msg["d"].(map[string]any)["session_id"].(string)
	if msg["s"] == nil {
		c.sequence = 0
	} else {
		c.sequence = int(msg["s"].(float64))
	}
	voiceStateUpdateData := msg["d"].(map[string]any)
	userId := voiceStateUpdateData["user_id"].(string)
	voiceChannelID := voiceStateUpdateData["channel_id"]
	if voiceChannelID == nil {
		delete(c.members, userId)
	} else {
		memberToUpdate, ok := c.members[userId]
		if ok {
			memberToUpdate.voiceChannelID = voiceChannelID.(string)
		} else {
			c.members[userId] = &member{User: user{Id: userId}, voiceChannelID: voiceChannelID.(string)}
		}
	}

	return nil
}

type voiceConnStruct struct {
	ServerID  string `json:"server_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
}

func (c *DiscordVoiceClient) connectToVoiceEndpoint() error {
	connectionURL := fmt.Sprintf("wss://%s?v=4", c.voiceEndpoint)

	log.Println("Connecting to voice endpoint: ", connectionURL)

	conn, _, err := websocket.DefaultDialer.DialContext(c.ctx, connectionURL, nil)
	if err != nil {
		log.Fatal("voice dial:", err)
	}

	log.Println("Connected to voice endpoint")

	c.voiceConn = conn

	for {
		if c.voiceSessionID != "" {
			break
		}
	}

	msg := payload{
		0,
		voiceConnStruct{
			ServerID:  c.cfg.ServerID,
			UserID:    c.UserID,
			SessionID: c.voiceSessionID,
			Token:     c.voiceToken,
		},
	}

	log.Println("sending voice json payload : ", fmt.Sprintf("%v", msg))

	err = c.voiceConn.WriteJSON(msg)
	if err != nil {
		return err
	}

	c.readVoiceEndpoint()

	return nil
}

func (c *DiscordVoiceClient) readVoiceEndpoint() {
	go func() {
		defer func(Conn *websocket.Conn) {
			err := Conn.Close()
			if err != nil {
				log.Println(err)
			}
		}(c.voiceConn)

		for {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			_, message, err := c.voiceConn.ReadMessage()
			if err != nil {
				log.Fatal("voice msg read:", err)
			}

			var m map[string]any
			err = json.Unmarshal(message, &m)
			if err != nil {
				log.Println(err)
				return
			}
			log.Println("voice endpoint recv:", string(message))

			switch int(m["op"].(float64)) {
			case 0:
				//
			case 1:
				//
			case 2:
				voiceIP := m["d"].(map[string]any)["ip"].(string)
				voicePort := int(m["d"].(map[string]any)["port"].(float64))
				SSRC := uint32(m["d"].(map[string]any)["ssrc"].(float64))

				raddr, err := net.ResolveUDPAddr("udp", voiceIP+":"+strconv.Itoa(voicePort))
				if err != nil {
					log.Fatal(err)
				}
				UDPconn, err := net.DialUDP("udp", nil, raddr)
				if err != nil {
					log.Fatal(err)
				}

				sb := make([]byte, 70)
				binary.BigEndian.PutUint32(sb, SSRC)
				_, err = UDPconn.Write(sb)
				if err != nil {
					log.Fatal(err)
				}

				rb := make([]byte, 70)
				rlen, _, err := UDPconn.ReadFromUDP(rb)
				if err != nil {
					log.Fatal(err)
				}
				if rlen < 70 {
					log.Fatal("too short response from UDP connection")
				}

				var ip string
				for i := 4; i < 20; i++ {
					if rb[i] == 0 {
						break
					}
					ip += string(rb[i])
				}

				port := binary.BigEndian.Uint16(rb[68:70])
				i, _ := strconv.Atoi(strings.Split(UDPconn.LocalAddr().String(), ":")[1])

				log.Println("IP:", ip)
				log.Println("PORT:", port)

				js := payload{
					1,
					map[string]any{
						"protocol": "udp",
						"data": map[string]any{
							"address": ip,
							"port":    uint16(i),
							"mode":    "xsalsa20_poly1305"}}}
				log.Println("UDP CONN:", UDPconn.LocalAddr())

				err = c.voiceConn.WriteJSON(js)
				if err != nil {
					log.Fatal(err)
				}
			case 3:
				//
			case 4:
				arr := [32]byte{}
				for i, v := range m["d"].(map[string]any)["secret_key"].([]any) {
					arr[i] = byte(v.(float64))
				}
				c.secretKey = arr
				log.Println("SECRET KEY:", c.secretKey)
			case 5:
				//
			case 7:
				//
			case 8:
				c.voiceHeartBeatInterval = int(m["d"].(map[string]any)["heartbeat_interval"].(float64))
				go func() {
					ticker := time.NewTicker(time.Duration(c.voiceHeartBeatInterval) * time.Millisecond)

					for {
						js := payload{3, c.sequence}
						err := c.voiceConn.WriteJSON(js)
						if err != nil {
							log.Fatal("voice hb:", err)
						}

						select {
						case <-c.ctx.Done():
							return
						case <-ticker.C:
						}
					}
				}()
			case 9:
				//
			}
		}
	}()
}

func (client *DiscordVoiceClient) UpdateServerInfo(guildCreateResponse map[string]any) {
	members := guildCreateResponse["d"].(map[string]any)["members"].([]interface{})
	var ids []string
	for _, v := range members {
		ids = append(ids, v.(map[string]any)["user"].(map[string]interface{})["id"].(string))
	}
	for _, v := range ids {
		_, ok := client.members[v]
		if !ok {
			client.members[v] = &member{User: user{Id: v}}
		}
	}

	voiceStates := guildCreateResponse["d"].(map[string]any)["voice_states"].([]interface{})
	for _, v := range voiceStates {
		id := v.(map[string]any)["user_id"].(string)
		_, ok := client.members[id]
		if ok {
			m := client.members[id]
			m.voiceChannelID = v.(map[string]any)["channel_id"].(string)
		}
	}
}
