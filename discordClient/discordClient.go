package discordClient

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/nacl/secretbox"
	"gopkg.in/hraban/opus.v2"
)

/*

TODO

- sending voice
- heartbeat for voiceEndpoint
- add errors handling and errors creating

*/

type DiscordClient struct {
	ClientId string
	BotToken string

	HeartBeatInterval      int
	VoiceHeartBeatInterval int
	Conn                   *websocket.Conn
	VoiceConn              *websocket.Conn

	UDPConn *net.Conn

	VoiceUDPIp   string
	VoiceUDPPort int
	SSRC         uint32

	MyIp   string
	MyPort uint16

	SecretKey []byte

	GuildId   string
	SessionId string
	UserId    string
	ServerId  string
	Token     string

	VoiceEndpoint string

	muS sync.Mutex
	s   int

	muRead  sync.Mutex
	muWrite sync.Mutex

	muVoiceRead  sync.Mutex
	muVoiceWrite sync.Mutex
}

const (
	DISPATCH             = 0
	HEARTBEAT            = 1
	IDENTIFY             = 2
	PRESENCE_UPDATE      = 3
	VOICE_STATE_UPDATE   = 4
	RESUME               = 6
	RECONNECT            = 7
	REQUEST_GUILD_MEMBER = 8
	INVALID_SESSION      = 9
	HELLO                = 10
	HEARTBEAT_ACK        = 11
)

const (
	PERMISSIONS = "2080374774"
	API_URL     = "https://discord.com/api"
	SAMPLE_RATE = 48000
	CHANNELS    = 2
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var gatewayUrl string

func (d *DiscordClient) GetAuthLink() string {
	return API_URL + "/oauth2/authorize?client_id=" + d.ClientId + "&scope=bot&permissions=" + PERMISSIONS + "&redirect_uri=http%3A%2F%localhost%3A8080"
}

func NewDiscordClient(clientId, botToken string) DiscordClient {
	return DiscordClient{ClientId: clientId, BotToken: botToken}
}

func (d *DiscordClient) Gateway() {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", API_URL+"/gateway/bot", nil)
	req.Header.Add("Authorization", "Bot "+d.BotToken)
	res, _ := client.Do(req)
	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	newStr := buf.String()
	var result map[string]interface{}
	json.Unmarshal([]byte(newStr), &result)

	gatewayUrl = result["url"].(string)
	log.Println("Connecting to", gatewayUrl)

	conn, _, err := websocket.DefaultDialer.Dial(gatewayUrl+"/?v=6&encoding=json", nil)

	if err != nil {
		log.Fatal("dial:", err)
	}

	d.Conn = conn

	d.readPump(d.Conn)
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
	//js := fmt.Sprintf("{\"op\":2,\"d\":{\"token\":\"%s\",\"properties\":{\"$os\":\"linux\",\"$browser\":\"my_library\",\"$device\":\"my_library\"}}}", d.BotToken)
	js := payload{2, d{client.BotToken, properties{"linux", "lib", "lib"}}}
	log.Println(js)
	client.muWrite.Lock()
	client.Conn.WriteJSON(js)
	client.muWrite.Unlock()
}

func (client *DiscordClient) readPump(conn *websocket.Conn) {
	go func() {
		for {
			client.muRead.Lock()
			_, message, err := conn.ReadMessage()
			client.muRead.Unlock()

			if err != nil {
				log.Println("read error:", err)
				return
			}
			var m map[string]interface{}
			json.Unmarshal(message, &m)
			log.Println("recv:", string(message))
			log.Println()
			switch int(m["op"].(float64)) {
			case DISPATCH:
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
					client.muS.Unlock() //client.muS.Lock()
					//client.s = map["s"]
					//client.muS.Unlock()
					//log.Println("GATHERED:", client.SessionId)
				}
			case HEARTBEAT:
				//
			case IDENTIFY:
				//
			case PRESENCE_UPDATE:
				//
			case VOICE_STATE_UPDATE:
				//
			case RESUME:
				//
			case RECONNECT:
				//
			case REQUEST_GUILD_MEMBER:
				//
			case INVALID_SESSION:
				//
			case HELLO:
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
			case HEARTBEAT_ACK:
				client.muS.Lock()
				if m["s"] == nil {
					client.s = 0
				} else {
					client.s = int(m["s"].(float64))
				}
				client.muS.Unlock()
				log.Println("ACK RECEIVED")
			}
		}
	}()
}

func (client *DiscordClient) GetVoiceRegions() {
	httpclient := &http.Client{}
	req, _ := http.NewRequest("GET", API_URL+"/voice/regions", nil)
	req.Header.Add("Authorization", "Bot "+client.BotToken)
	res, _ := httpclient.Do(req)
	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	newStr := buf.String()
	var result map[string]interface{}
	json.Unmarshal([]byte(newStr), &result)

	log.Println("VOICE REGION:")
}

type wrap struct {
	v voiceStruct `json:"d"`
}

type voiceStruct struct {
	GuildId   string `json:"guild_id"`
	ChannelId string `json:"channel_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

func (client *DiscordClient) RetrieveVoiceServerInformation() {
	httpclient := &http.Client{}
	req, _ := http.NewRequest("GET", API_URL+"/users/@me/guilds", nil)
	req.Header.Add("Authorization", "Bot "+client.BotToken)
	res, _ := httpclient.Do(req)
	buf := new(bytes.Buffer)
	buf.ReadFrom(res.Body)
	newStr := buf.String()
	newStr = newStr[1 : len(newStr)-1]
	var result map[string]interface{}
	json.Unmarshal([]byte(newStr), &result)

	client.GuildId = result["id"].(string)

	// TODO
	// somehow gather channelId
	var channelId string
	channelId = "741230309441404952"

	js := payload{4, voiceStruct{client.GuildId, channelId, false, false}}
	j, _ := json.Marshal(js)
	log.Println(string(j))

	client.muWrite.Lock()
	client.Conn.WriteJSON(js)
	client.muWrite.Unlock()
}

type voiceConnStruct struct {
	ServerId  string `json:"server_id"`
	UserId    string `json:"user_id"`
	SessionId string `json:"session_id"`
	Token     string `json:"token"`
}

func (client *DiscordClient) ConnectToVoiceEndpoint() {
	client.ServerId = "741230309441404948"
	conn, _, err := websocket.DefaultDialer.Dial("wss://"+client.VoiceEndpoint[:len(client.VoiceEndpoint)-3]+"?v=4", nil)
	client.VoiceConn = conn
	if err != nil {
		log.Fatal("voice dial:", err)
	}

	js := payload{0, voiceConnStruct{client.ServerId, client.UserId, client.SessionId, client.Token}}

	client.muVoiceWrite.Lock()
	client.VoiceConn.WriteJSON(js)
	client.muVoiceWrite.Unlock()

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
		defer client.Conn.Close()
		for {

			client.muVoiceRead.Lock()
			_, message, err := conn.ReadMessage()
			client.muVoiceRead.Unlock()
			if err != nil {
				log.Fatal("voice msg read:", err)
			}

			var m map[string]interface{}
			json.Unmarshal(message, &m)
			log.Println("voice endpoint recv:", string(message))
			log.Println()

			switch int(m["op"].(float64)) {
			case 0:
				log.Println("OP CODE 1 RECEIVED")
			case 2:
				client.VoiceUDPIp = m["d"].(map[string]interface{})["ip"].(string)
				client.VoiceUDPPort = int(m["d"].(map[string]interface{})["port"].(float64))
				client.SSRC = uint32(m["d"].(map[string]interface{})["ssrc"].(float64))

				conn, err := net.Dial("udp", client.VoiceUDPIp+":"+strconv.Itoa(client.VoiceUDPPort))
				if err != nil {
					log.Fatal(err)
					return
				}

				client.UDPConn = &conn
				buff := []byte{0, 1, 0, 70}
				if err != nil {
					log.Println(err)
				}
				a := make([]byte, 4)
				binary.BigEndian.PutUint32(a, client.SSRC)
				buff = append(buff, a...)
				for i := 0; i < 66; i++ {
					buff = append(buff, 0)
				}
				_, err = conn.Write(buff)
				log.Println("SIEMA")
				time.Sleep(time.Second)
				buff = make([]byte, 100)
				conn.SetDeadline(time.Now().Add(time.Second))
				_, err = conn.Read(buff)
				log.Println("IP DISCOVERY")
				log.Println(buff)
				client.MyIp = string(buff[8:72])
				client.MyPort = binary.BigEndian.Uint16(buff[72:74])

				js := payload{1, OneOp{"udp", Data{client.MyIp, client.MyPort, "xsalsa20_poly1305"}}}

				client.muVoiceWrite.Lock()
				err = client.VoiceConn.WriteJSON(js)
				client.muVoiceWrite.Unlock()
				if err != nil {
					log.Fatal(err)
				}
			case 4:
				arr := make([]byte, 32)
				for i, v := range m["d"].(map[string]interface{})["secret_key"].([]interface{}) {
					arr[i] = byte(v.(float64))
				}
				client.SecretKey = arr
				log.Println("SECRETY KEY:", client.SecretKey)
			case 8:
				client.VoiceHeartBeatInterval = int(m["d"].(map[string]interface{})["heartbeat_interval"].(float64))
				go func() {
					for {
						js := payload{3, client.s}
						client.muVoiceWrite.Lock()
						err := client.VoiceConn.WriteJSON(js)
						client.muVoiceWrite.Unlock()
						if err != nil {
							log.Fatal("voice hb:", err)
						}
						log.Println("VOICE HD SENT")
						time.Sleep(time.Duration(client.VoiceHeartBeatInterval) * time.Millisecond)
					}
				}()
			}
		}
	}()
}

type speaking struct {
	Speaking int `json:"speaking"`
	Dealy    int `json:"delay"`
	Ssrc     int `json:"ssrc"`
}

func (client *DiscordClient) SendLambo() {
	js := payload{5, speaking{5, 0, 1}}
	client.muVoiceWrite.Lock()
	client.VoiceConn.WriteJSON(js)
	client.muVoiceWrite.Unlock()

	client.sendSound("/home/js/Downloads/lemon")
}

func (client *DiscordClient) sendSound(fname string) {
	sequence := rand.Intn(530)
	timestamp := rand.Intn(500)
	f, err := os.Open(fname)
	if err != nil {
		log.Fatal(err)
		return
	}
	s, err := opus.NewStream(f)
	if err != nil {
		log.Fatal(err)
		return
	}
	defer s.Close()
	buf := make([]int16, 16384)
	for {
		n, err := s.Read(buf)
		log.Println("FRAME", n)
		if err != nil {
			break
		} else if err != nil {
			log.Fatal(err)
			return
		}

		var data []byte
		a := make([]byte, 2)
		b := make([]byte, 4)
		binary.BigEndian.PutUint16(a, uint16(sequence))
		binary.BigEndian.PutUint32(b, uint32(timestamp))
		binary.BigEndian.PutUint32(b, uint32(client.SSRC))
		data = append(data, 0x80)
		data = append(data, 0x78)
		data = append(data, a...)
		data = append(data, b...)
		data = append(data, b...)

		var nonce [24]byte
		for i := 0; i < 12; i++ {
			nonce[i] = data[i]
			nonce[12+i] = 0
		}
		var key [32]byte
		for i := 0; i < 32; i++ {
			key[i] = client.SecretKey[i]
		}
		var arr []byte
		for i := 0; i < n*CHANNELS; i++ {
			binary.BigEndian.PutUint16(a, uint16(buf[i]))
			arr = append(arr, a...)
		}

		data = secretbox.Seal(data, arr, &nonce, &key)
		(*client.UDPConn).Write(data)
		timestamp += 20
		sequence++
		time.Sleep(time.Duration(20) * time.Millisecond)
	}
}

type heartbeat struct {
	Op int `json:"op"`
	D  int `json:"d"`
}

func (client *DiscordClient) sendHeartBeatEvery(milis int) {
	for {
		log.Println("ABOUT TO SEND HB")
		var js heartbeat
		client.muS.Lock()
		js = heartbeat{1, client.s}
		client.muS.Unlock()
		client.muWrite.Lock()
		err := client.Conn.WriteJSON(js)
		client.muWrite.Unlock()

		if err != nil {
			log.Println("error:", err)
		}
		log.Println("HB SENT")
		time.Sleep(time.Duration(milis) * time.Millisecond)

	}
}

func (client *DiscordClient) Close() {
	client.Conn.Close()
	client.VoiceConn.Close()
}
