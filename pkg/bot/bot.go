package bot

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bsponge/discordBot/pkg/config"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/nacl/secretbox"
	"gopkg.in/hraban/opus.v2"
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
)

const (
	Permissions = "8"
	ApiURL      = "https://discord.com/api"
	SampleRate  = 48000
	Channels    = 2

	authLinkTemplate = "%s/oauth2/authorize?client_id=%s&scope=bot&permissions=%s&redirect_uri=http%3A%2F%localhost%3A8080"
)

var gatewayUrl string

type DiscordClient struct {
	ctx context.Context

	cfg *config.Config

	HeartBeatInterval      int
	VoiceHeartBeatInterval int
	Conn                   *websocket.Conn
	VoiceConn              *websocket.Conn

	UDPConn    *net.UDPConn
	UDPAddress *net.UDPAddr

	VoiceUDPIp   string
	VoiceUDPPort int
	SSRC         uint32

	SecretKey [32]byte

	GuildId   string
	SessionId string
	UserId    string
	Token     string

	VoiceEndpoint string

	muS sync.Mutex
	s   int

	muRead  sync.Mutex
	muWrite sync.Mutex

	muVoiceRead  sync.Mutex
	muVoiceWrite sync.Mutex

	Sequence uint32

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

func (client *DiscordClient) Start(ctx context.Context) error {
	client.ctx = ctx

	httpClient := &http.Client{}

	req, err := http.NewRequest("GET", ApiURL+"/gateway/bot", nil)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Add("Authorization", "Bot "+client.cfg.Token)
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
	var result map[string]interface{}
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

	client.Conn = conn

	go client.readPump(ctx, client.Conn)

	return nil
}

type payload struct {
	Op int         `json:"op"`
	D  interface{} `json:"d"`
}

type d struct {
	Token      string     `json:"token"`
	Properties properties `json:"properties"`
}

type properties struct {
	Os      string `json:"$os"`
	Browser string `json:"$browser"`
	Device  string `json:"$device"`
}

func (client *DiscordClient) identify() {
	js := payload{2, d{client.cfg.Token, properties{"linux", "lib", "lib"}}}
	client.muWrite.Lock()
	defer client.muWrite.Unlock()
	err := client.Conn.WriteJSON(js)
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

type member struct {
	User           user          `json:"user"`
	Mute           bool          `json:"mute"`
	Deaf           bool          `json:"deaf"`
	Roles          []interface{} `json:"roles"`
	JoinedAt       string        `json:"joined_at"`
	HoisedRole     interface{}   `json:"hoisted_role"`
	voiceChannelId string
}

func (client *DiscordClient) updateServerInfo(guildCreateResponse map[string]interface{}) {
	members := guildCreateResponse["d"].(map[string]interface{})["members"].([]interface{})
	var ids []string
	for _, v := range members {
		ids = append(ids, v.(map[string]interface{})["user"].(map[string]interface{})["id"].(string))
	}
	for _, v := range ids {
		_, ok := client.members[v]
		if !ok {
			client.members[v] = &member{User: user{Id: v}}
		}
	}

	voiceStates := guildCreateResponse["d"].(map[string]interface{})["voice_states"].([]interface{})
	for _, v := range voiceStates {
		id := v.(map[string]interface{})["user_id"].(string)
		_, ok := client.members[id]
		if ok {
			m := client.members[id]
			m.voiceChannelId = v.(map[string]interface{})["channel_id"].(string)
		}
	}
}

func (client *DiscordClient) readPump(ctx context.Context, conn *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		client.muRead.Lock()
		_, message, err := conn.ReadMessage()
		client.muRead.Unlock()

		if err != nil {
			log.Println("read error:", err)
			return
		}
		var m map[string]interface{}
		err = json.Unmarshal(message, &m)
		if err != nil {
			return
		}

		log.Println("received:", string(message))

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

func (client *DiscordClient) handleDispatch(m map[string]interface{}) {
	t := m["t"].(string)
	client.muS.Lock()
	defer client.muS.Unlock()

	switch t {
	case "READY":
		client.UserId = m["d"].(map[string]interface{})["user"].(map[string]interface{})["id"].(string)
		client.SessionId = m["d"].(map[string]interface{})["session_id"].(string)

		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
	case "GUILD_CREATE":
		client.cfg.ServerID = m["d"].(map[string]interface{})["id"].(string)
		client.updateServerInfo(m)
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
	case "VOICE_SERVER_UPDATE":
		client.VoiceEndpoint = m["d"].(map[string]interface{})["endpoint"].(string)
		client.Token = m["d"].(map[string]interface{})["token"].(string)
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
		client.ConnectToVoiceEndpoint()
	case "VOICE_STATE_UPDATE":
		client.SessionId = m["d"].(map[string]interface{})["session_id"].(string)
		if m["s"] == nil {
			client.s = 0
		} else {
			client.s = int(m["s"].(float64))
		}
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
	log.Println("Handle hello ")
	client.HeartBeatInterval = int(m["d"].(map[string]interface{})["heartbeat_interval"].(float64))
	client.identify()
	go client.sendHeartBeatEvery(client.HeartBeatInterval)
	client.muS.Lock()
	defer client.muS.Unlock()
	if m["s"] == nil {
		client.s = 0
	} else {
		client.s = int(m["s"].(float64))
	}
}

func (client *DiscordClient) handleHeartbeatAck(m map[string]interface{}) {
	client.muS.Lock()
	defer client.muS.Unlock()
	if m["s"] == nil {
		client.s = 0
	} else {
		client.s = int(m["s"].(float64))
	}
}

func (client *DiscordClient) listenForMusicRequests() {
	for {
		select {
		case <-client.ctx.Done():
			return
		case requests := <-client.musicQueue:
			<-client.finishedPlaying
			go client.SendAudio(requests)
		}
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
	var result map[string]interface{}
	err = json.Unmarshal([]byte(newStr), &result)
	if err != nil {
		log.Println("Could not unmarshal voice regions resposne: ", err)
		return
	}
}

type voiceStruct struct {
	GuildId   string `json:"guild_id"`
	ChannelId string `json:"channel_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

func (client *DiscordClient) ConnectToVoiceChannel(channelId string) {
	httpclient := &http.Client{}
	req, err := http.NewRequest("GET", ApiURL+"/users/@me/guilds", nil)
	if err != nil {
		panic(err)
	}
	req.Header.Add("Authorization", "Bot "+client.cfg.Token)
	req = req.WithContext(client.ctx)

	res, err := httpclient.Do(req)
	if err != nil {
		log.Printf("Could not connect to voice channel %s: %w", channelId, err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(res.Body)
	if err != nil {
		log.Println("Could not read response of connect to voice channel request: ", err)
		return
	}

	newStr := buf.String()
	newStr = newStr[1 : len(newStr)-1]
	var result map[string]interface{}
	err = json.Unmarshal([]byte(newStr), &result)
	if err != nil {
		log.Println("Could not unmarshal response of voice channel response: ", err)
		return
	}

	client.GuildId = result["id"].(string)

	js := payload{4, voiceStruct{client.GuildId, channelId, false, false}}
	j, err := json.Marshal(js)
	if err != nil {
		log.Println("Could not unmarshal: ", err)
		return
	}
	log.Println(string(j))

	client.muWrite.Lock()
	defer client.muWrite.Unlock()
	err = client.Conn.WriteJSON(js)
	if err != nil {
		log.Println("Could not send voice connection info: ", err)
		return
	}
}

type voiceConnStruct struct {
	ServerID  string `json:"server_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
}

func (client *DiscordClient) ConnectToVoiceEndpoint() {
	connectionURL := fmt.Sprintf("wss://%s?v=4", client.VoiceEndpoint)

	log.Println("Connecting to voice endpoint: ", connectionURL)

	conn, _, err := websocket.DefaultDialer.DialContext(client.ctx, connectionURL, nil)
	if err != nil {
		log.Fatal("voice dial:", err)
	}

	log.Println("Connected to voice endpoint")

	client.VoiceConn = conn

	js := payload{0, voiceConnStruct{
		ServerID:  client.cfg.ClientID,
		UserID:    client.UserId,
		SessionID: client.SessionId,
		Token:     client.Token,
	}}

	log.Println("token: ", client.Token)

	err = conn.WriteJSON(js)
	if err != nil {
		log.Println("Could not send voice connection info: ", err)
		return
	}

	client.readVoiceEndpoint(client.VoiceConn)
}

type OneOp struct {
	Protocol string      `json:"protocol"`
	Data     interface{} `json:"data"`
}

type Data struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	Mode    string `json:"mode"`
}

func (client *DiscordClient) readVoiceEndpoint(conn *websocket.Conn) {
	go func() {
		defer func(Conn *websocket.Conn) {
			err := Conn.Close()
			if err != nil {
				log.Println(err)
			}
		}(client.Conn)
		for {
			select {
			case <-client.ctx.Done():
				return
			default:
			}

			client.muVoiceRead.Lock()
			_, message, err := conn.ReadMessage()
			client.muVoiceRead.Unlock()
			if err != nil {
				log.Fatal("voice msg read:", err)
			}

			var m map[string]interface{}
			err = json.Unmarshal(message, &m)
			if err != nil {
				log.Println(err)
			}
			log.Println("voice endpoint recv:", string(message))

			switch int(m["op"].(float64)) {
			case 0:
				//
			case 1:
				//
			case 2:
				client.VoiceUDPIp = m["d"].(map[string]interface{})["ip"].(string)
				client.VoiceUDPPort = int(m["d"].(map[string]interface{})["port"].(float64))
				client.SSRC = uint32(m["d"].(map[string]interface{})["ssrc"].(float64))

				raddr, err := net.ResolveUDPAddr("udp", client.VoiceUDPIp+":"+strconv.Itoa(client.VoiceUDPPort))
				if err != nil {
					log.Fatal(err)
				}
				conn, err := net.DialUDP("udp", nil, raddr)
				if err != nil {
					log.Fatal(err)
				}

				client.UDPAddress = raddr

				client.UDPConn = conn

				sb := make([]byte, 70)
				binary.BigEndian.PutUint32(sb, client.SSRC)
				_, err = client.UDPConn.Write(sb)
				if err != nil {
					log.Fatal(err)
				}

				rb := make([]byte, 70)
				rlen, _, err := client.UDPConn.ReadFromUDP(rb)
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
				i, _ := strconv.Atoi(strings.Split(client.UDPConn.LocalAddr().String(), ":")[1])

				log.Println("IP:", ip)
				log.Println("PORT:", port)

				js := payload{
					1,
					map[string]interface{}{
						"protocol": "udp",
						"data": map[string]interface{}{
							"address": ip,
							"port":    uint16(i),
							"mode":    "xsalsa20_poly1305"}}}
				log.Println("UDP CONN:", client.UDPConn.LocalAddr())

				client.muVoiceWrite.Lock()
				err = client.VoiceConn.WriteJSON(js)
				client.muVoiceWrite.Unlock()
				if err != nil {
					log.Fatal(err)
				}
			case 3:
				//
			case 4:
				arr := [32]byte{}
				for i, v := range m["d"].(map[string]interface{})["secret_key"].([]interface{}) {
					arr[i] = byte(v.(float64))
				}
				client.SecretKey = arr
				log.Println("SECRET KEY:", client.SecretKey)
			case 5:
				//
			case 7:
				//
			case 8:
				client.VoiceHeartBeatInterval = int(m["d"].(map[string]interface{})["heartbeat_interval"].(float64))
				go func() {
					ticker := time.NewTicker(time.Duration(client.VoiceHeartBeatInterval) * time.Millisecond)

					for {
						js := payload{3, client.s}
						client.muVoiceWrite.Lock()
						err := client.VoiceConn.WriteJSON(js)
						client.muVoiceWrite.Unlock()
						if err != nil {
							log.Fatal("voice hb:", err)
						}

						select {
						case <-client.ctx.Done():
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
		log.Fatal("Youtube-dl error: ", err)
	}
	return stdout
}

func (client *DiscordClient) SendAudio(videoName []string) {
	stdout := getAudioUrl(videoName)

	js := payload{5, speaking{5, 0, 0}}

	func() {
		client.muVoiceWrite.Lock()
		defer client.muVoiceWrite.Unlock()

		err := client.VoiceConn.WriteJSON(js)
		if err != nil {
			log.Println("voiceConn write json error")
			log.Fatal(err)
		}
	}()

	quit := make(chan bool)
	client.QuitAudioCh = quit
	quitAudio := make(chan bool)

	audioC := client.soundEncoder(string(stdout[:len(stdout)-1]), quit)
	client.soundSender(audioC, quitAudio, 960, SampleRate, quit)
}

func (client *DiscordClient) soundEncoder(url string, quit chan bool) chan []byte {
	c := make(chan []byte)

	go func(client *DiscordClient) {
		pcmBuffer := make([]int16, 16384)
		enc, err := opus.NewEncoder(48000, 2, opus.AppAudio)
		if err != nil {
			log.Println("Cannot create opus encoder")
			log.Fatal(err)
		}
		audioReader := ReadAudioFromUrl(url)
		s, err := opus.NewStream(audioReader)
		if err != nil {
			log.Println("Cannot create new opus stream")
			quit <- true
			client.finishedPlaying <- true
			return
		}
		defer func(s *opus.Stream) {
			err := s.Close()
			if err != nil {
				log.Println(err)
			}
		}(s)
		defer close(c)
		for {
			n, err := s.Read(pcmBuffer)
			if err == io.EOF {
				return
			} else if err != nil {
				log.Println("Cannot read from opus stream")
				log.Fatal(err)
			}
			if n == 960 {
				pcm := pcmBuffer[:n*Channels]
				data := make([]byte, 2000)
				n, err = enc.Encode(pcm, data)
				if err != nil {
					log.Fatal(err)
				}
				data = data[:n]
				select {
				case c <- data:
				case <-quit:
					return
				}
			} else if n == 0 {
				return
			}
		}
	}(client)

	return c
}

func (client *DiscordClient) soundSender(audioChan <-chan []byte, quitAudio <-chan bool, frameSize, sampleRate int, quit <-chan bool) {
	sequence := client.Sequence
	timestamp := uint32(rand.Intn(530))

	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = 0x78

	nonce := [24]byte{}

	timeIncr := uint32(frameSize / (sampleRate / 1000))

	ticker := time.NewTicker(time.Millisecond * time.Duration(timeIncr))

	var recvAudio []byte

	defer func(client *DiscordClient) {
		client.finishedPlaying <- true
	}(client)

	for {
		binary.BigEndian.PutUint32(header[8:], client.SSRC)
		binary.BigEndian.PutUint32(header[4:], timestamp)
		binary.BigEndian.PutUint16(header[2:], uint16(sequence))

		select {
		case a, ok := <-audioChan:
			if !ok {
				silenceFrames := []byte{0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe}
				copy(nonce[:12], header)
				send := secretbox.Seal(header, silenceFrames, &nonce, &client.SecretKey)

				_, err := client.UDPConn.Write(send)
				if err != nil {
					log.Fatal(err)
				}

				js := payload{5, speaking{0, 0, int(client.SSRC)}}
				client.muVoiceWrite.Lock()
				err = client.VoiceConn.WriteJSON(js)
				if err != nil {
					log.Fatal(err)
				}
				client.muVoiceWrite.Unlock()

				client.Sequence = sequence

				return
			}
			recvAudio = a
		case <-quitAudio:
			return
		case <-quit:
			return
		case <-client.ctx.Done():
			return
		}

		copy(nonce[:12], header)

		send := secretbox.Seal(header, recvAudio, &nonce, &client.SecretKey)

		select {
		case <-quit:
			silenceFrames := []byte{0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe, 0xf8, 0xff, 0xfe}
			copy(nonce[:12], header)
			send := secretbox.Seal(header, silenceFrames, &nonce, &client.SecretKey)

			_, err := client.UDPConn.Write(send)
			if err != nil {
				log.Fatal(err)
			}

			js := payload{5, speaking{0, 0, int(client.SSRC)}}
			client.muVoiceWrite.Lock()
			err = client.VoiceConn.WriteJSON(js)
			if err != nil {
				log.Fatal(err)
			}
			client.muVoiceWrite.Unlock()
			client.Sequence = sequence
			return
		case <-ticker.C:
			//
		}

		_, err := client.UDPConn.Write(send)
		if err != nil {
			log.Fatal(err)
		}
		timestamp += uint32(frameSize)
		sequence++
	}
}

type heartbeat struct {
	Op int `json:"op"`
	D  int `json:"d"`
}

func (client *DiscordClient) sendHeartBeatEvery(milis int) {
	ticker := time.NewTicker(time.Duration(milis) * time.Millisecond)

	for {
		log.Println("Will send heartbeat")

		var js heartbeat
		client.muS.Lock()
		js = heartbeat{1, client.s}
		client.muS.Unlock()
		client.muWrite.Lock()
		err := client.Conn.WriteJSON(js)
		client.muWrite.Unlock()

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
	client.muWrite.Lock()
	defer client.muWrite.Unlock()
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
