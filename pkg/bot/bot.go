package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bsponge/discordBot/pkg/config"

	"github.com/gorilla/websocket"
)

const (
	Dispatch           = 0
	Heartbeat          = 1
	Identify           = 2
	PresenceUpdate     = 3
	VoiceStateUpdate   = 4
	Resume             = 6
	Reconnect          = 7
	RequestGuildMember = 8
	InvalidSession     = 9
	Hello              = 10
	HeartbeatACK       = 11

	ReadyDispatch             = "READY"
	GuildCreateDispatch       = "GUILD_CREATE"
	VoiceServerUpdateDispatch = "VOICE_SERVER_UPDATE"
	VoiceStateUpdateDispatch  = "VOICE_STATE_UPDATE"
	MessageCreateDispatch     = "MESSAGE_CREATE"
)

const (
	Permissions = "8"
	ApiURL      = "https://discord.com/api"
	SampleRate  = 48000
	Channels    = 2

	authLinkTemplate = `%s/oauth2/authorize?client_id=%s&scope=bot&permissions=%s&redirect_uri=http%3A%2F%localhost%3A8080`
)

type DiscordClient struct {
	ctx context.Context

	cfg *config.Config

	voiceClient *DiscordVoiceClient

	HeartBeatInterval      int
	VoiceHeartBeatInterval int
	Conn                   *websocket.Conn
	VoiceConn              *websocket.Conn

	VoiceUDPIp   string
	VoiceUDPPort int
	SSRC         uint32

	SecretKey [32]byte

	GuildID        string
	SessionID      string
	VoiceSessionID string
	UserID         string
	Token          string

	VoiceEndpoint string

	mtx sync.Mutex

	Sequence uint32
	s        int

	QuitAudioCh chan bool

	members map[string]*member

	musicQueue      chan []string
	finishedPlaying chan bool
}

func (client *DiscordClient) GetAuthLink() string {
	return fmt.Sprintf(authLinkTemplate, ApiURL, client.cfg.ClientID, Permissions)
}

func NewDiscordClient() (*DiscordClient, error) {
	cfg, err := config.NewConfig()
	if err != nil {
		return nil, err
	}

	musicQueue := make(chan []string)
	finishedPlaying := make(chan bool, 1)

	return &DiscordClient{
		cfg:             cfg,
		members:         make(map[string]*member),
		musicQueue:      musicQueue,
		finishedPlaying: finishedPlaying,
	}, nil
}

func (c *DiscordClient) SendMessage(msg any) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	err := c.Conn.WriteJSON(msg)
	if err != nil {
		return nil
	}

	return nil
}

func (c *DiscordClient) Start(ctx context.Context) error {
	c.ctx = ctx

	httpClient := &http.Client{}

	req, err := http.NewRequest("GET", ApiURL+"/gateway/bot", nil)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Add("Authorization", "Bot "+c.cfg.Token)
	req = req.WithContext(ctx)

	res, err := httpClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(res.Body)
	if err != nil {
		log.Println(err)
	}

	newStr := buf.String()
	var result map[string]any
	err = json.Unmarshal([]byte(newStr), &result)
	if err != nil {
		log.Println(err)
	}

	gatewayURLValue, ok := result["url"]
	if !ok {
		return fmt.Errorf("could not obtain gateway URL")
	}

	gatewayURL := fmt.Sprint(gatewayURLValue)
	if gatewayURL == "" {
		return fmt.Errorf("gateway URL is empty")
	}

	log.Println("Connecting to", gatewayURL)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, gatewayURL+"/?v=6&encoding=json", nil)
	if err != nil {
		log.Fatal("dial:", err)
	}

	c.Conn = conn

	voiceClient, err := NewDiscordVoiceClient(c.cfg, c)
	if err != nil {
		return err
	}

	c.voiceClient = voiceClient

	go c.listenForMessages()

	return nil
}

type payload struct {
	Op int `json:"op"`
	D  any `json:"d"`
}

type d struct {
	Token      string     `json:"token"`
	Properties properties `json:"properties"`
}

type properties struct {
	Os      string `json:"os"`
	Browser string `json:"browser"`
	Device  string `json:"device"`
}

func (client *DiscordClient) identify() {
	js := payload{2, d{client.cfg.Token, properties{"linux", "lib", "lib"}}}
	err := client.SendMessage(js)
	if err != nil {
		log.Println("Received error while sending indentification info: ", err)
	}
}

type user struct {
	Username      string `json:"username"`
	Id            string `json:"id"`
	Discriminator string `json:"discriminator"`
	Avatar        string `json:"avatar"`
}

func (client *DiscordClient) listenForMessages() {
	for {
		select {
		case <-client.ctx.Done():
			return
		default:
		}

		var msg []byte
		var err error
		func() {
			client.mtx.Lock()
			defer client.mtx.Unlock()
			_, msg, err = client.Conn.ReadMessage()
		}()

		if err != nil {
			log.Println("read error:", err)
			return
		}

		var m map[string]any
		err = json.Unmarshal(msg, &m)
		if err != nil {
			return
		}

		log.Println("received:", string(msg))

		opCodeValue, ok := m["op"]
		if !ok {
			log.Println("received payload does not have \"op\" field")
			continue
		}

		opCode := int(opCodeValue.(float64))

		switch opCode {
		case Dispatch:
			client.handleDispatch(m)
		case Heartbeat:
		case Identify:
		case PresenceUpdate:
		case VoiceStateUpdate:
		case Resume:
		case Reconnect:
		case RequestGuildMember:
		case InvalidSession:
		case Hello:
			client.handleHello(m)
		case HeartbeatACK:
			client.handleHeartbeatAck(m)
		default:
			log.Println("Received unknown op code: ", opCode)
		}
	}
}

func (client *DiscordClient) handleDispatch(m map[string]any) {
	t := m["t"].(string)

	switch t {
	case ReadyDispatch:
		client.voiceClient.UserID = m["d"].(map[string]any)["user"].(map[string]interface{})["id"].(string)
		client.SessionID = m["d"].(map[string]any)["session_id"].(string)

		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
	case GuildCreateDispatch:
		client.cfg.ServerID = m["d"].(map[string]any)["id"].(string)
		client.voiceClient.UpdateServerInfo(m)
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
	case VoiceServerUpdateDispatch:
		err := client.voiceClient.HandleServerUpdateDispatch(m)
		if err != nil {
			log.Println(err)
			return
		}
	case VoiceStateUpdateDispatch:
		err := client.voiceClient.HandleStateUpdateDispatch(m)
		if err != nil {
			log.Print(err)
			return
		}
	case MessageCreateDispatch:
		msg := m["d"].(map[string]any)["content"].(string)
		action := strings.Split(msg, " ")

		if len(action) <= 1 {
			return
		}

		switch action[0] {
		case "play":
			if len(action) > 1 {
				userID := m["d"].(map[string]any)["author"].(map[string]interface{})["id"].(string)
				client.voiceClient.Start(client.ctx, userID)
				client.voiceClient.QueueRequest(strings.Join(action[1:], " "))
			}
		case "skip":
			client.QuitAudioCh <- true
		}
	}
}

func (client *DiscordClient) handleHello(m map[string]any) {
	client.HeartBeatInterval = int(m["d"].(map[string]any)["heartbeat_interval"].(float64))
	client.identify()
	go client.sendHeartBeatEvery(client.HeartBeatInterval)
	if m["s"] == nil {
		client.s = 0
	} else {
		client.s = int(m["s"].(float64))
	}
}

func (client *DiscordClient) handleHeartbeatAck(m map[string]any) {
	if m["s"] == nil {
		client.s = 0
	} else {
		client.s = int(m["s"].(float64))
	}
}

func (client *DiscordClient) GetVoiceRegions() {
	httpclient := &http.Client{}
	req, _ := http.NewRequest("GET", ApiURL+"/voice/regions", nil)
	req.Header.Add("Authorization", "Bot "+client.cfg.Token)
	req = req.WithContext(client.ctx)

	buf := new(bytes.Buffer)

	res, err := httpclient.Do(req)
	if err != nil {
		log.Println("Could not send obtain voice regions: ", err)
		return
	}

	_, err = buf.ReadFrom(res.Body)
	if err != nil {
		log.Println("Could not read voice regions from response: ", err)
		return
	}

	newStr := buf.String()
	var result map[string]any
	err = json.Unmarshal([]byte(newStr), &result)
	if err != nil {
		log.Println("Could not unmarshal voice regions resposne: ", err)
		return
	}
}

type OneOp struct {
	Protocol string `json:"protocol"`
	Data     any    `json:"data"`
}

type Data struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	Mode    string `json:"mode"`
}

type speaking struct {
	Speaking int `json:"speaking"`
	Delay    int `json:"delay"`
	Ssrc     int `json:"ssrc"`
}

func getAudioUrl(videoName []string) []byte {
	fileName := strings.Join(videoName, " ")
	args := []string{"--get-url", "-f", "250", "ytsearch:" + fileName}
	cmd := exec.Command("youtube-dl", args...)
	stdout, err := cmd.Output()
	if err != nil {
		log.Fatalf("Youtube-dl (cmd: %s) error: %s", cmd.String(), err)
	}
	return stdout
}

type heartbeat struct {
	Op int `json:"op"`
	D  int `json:"d"`
}

func (client *DiscordClient) sendHeartBeatEvery(milis int) {
	ticker := time.NewTicker(time.Duration(milis) * time.Millisecond)

	for {
		var js heartbeat
		js = heartbeat{1, client.s}

		err := client.SendMessage(js)
		if err != nil {
			log.Println("Received error while sending heartbeat:", err)
		}

		select {
		case <-client.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (client *DiscordClient) Close() {
	client.mtx.Lock()
	defer client.mtx.Unlock()
	if client.Conn != nil {
		err := client.Conn.Close()
		if err != nil {
			log.Println(err)
		}
	}
	if client.VoiceConn != nil {
		err := client.VoiceConn.Close()
		if err != nil {
			log.Println(err)
		}
	}
}
