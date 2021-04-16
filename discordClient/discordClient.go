package discordClient

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log"
  "io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
  "os/exec"

	"github.com/gorilla/websocket"
  "strings"
	"golang.org/x/crypto/nacl/secretbox"
	"gopkg.in/hraban/opus.v2"
)

/*

TODO

- sequence overflow handling 
- timestamp overflow handling

*/

type DiscordClient struct {
	ClientId string
	BotToken string

	HeartBeatInterval      int
	VoiceHeartBeatInterval int
	Conn                   *websocket.Conn
	VoiceConn              *websocket.Conn

	UDPConn *net.UDPConn

  UDPAddress *net.UDPAddr

	VoiceUDPIp   string
	VoiceUDPPort int
	SSRC         uint32

	SecretKey [32]byte

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

  Sequence uint32

  QuitAudioCh chan bool
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
	req, err := http.NewRequest("GET", API_URL+"/gateway/bot", nil)
  if err != nil {
    log.Fatal(err)
  }
	req.Header.Add("Authorization", "Bot " + d.BotToken)
	res, err := client.Do(req)
  if err != nil {
    log.Fatal(err)
  }
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
	js := payload{2, d{client.BotToken, properties{"linux", "lib", "lib"}}}
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
					client.muS.Unlock()
        case "MESSAGE_CREATE":
          msg := m["d"].(map[string]interface{})["content"].(string)
          action := strings.Split(msg, " ")
          switch action[0] {
          case "play":
            if len(action) > 1 {
              client.SendAudio(action[1:])
            }
          case "stop":
            client.QuitAudioCh <- true
          }
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
	req.Header.Add("Authorization", "Bot " + client.BotToken)
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
	conn, _, err := websocket.DefaultDialer.Dial("wss://"+client.VoiceEndpoint[:len(client.VoiceEndpoint)]+"?v=4", nil)
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
				log.Println("OP CODE 0 RECEIVED")
      case 1:
        log.Println("OP CODE 1 RECEIVED")
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

        js := payload{1, OneOp{"udp", Data{ip, uint16(i), "xsalsa20_poly1305"}}}
        log.Println("UDP CONN:", client.UDPConn.LocalAddr())

        client.muVoiceWrite.Lock()
        err = client.VoiceConn.WriteJSON(js)
        client.muVoiceWrite.Unlock()
				if err != nil {
					log.Fatal(err)
				}
      case 3:
        log.Println("OP CODE 3 RECEIVED")
			case 4:
				arr := [32]byte{}
				for i, v := range m["d"].(map[string]interface{})["secret_key"].([]interface{}) {
					arr[i] = byte(v.(float64))
				}
        client.SecretKey = arr
				log.Println("SECRETY KEY:", client.SecretKey)
      case 5:
        log.Println("OP CODE 5 RECEIVED")
      case 7:
        log.Println("OP CODE 7 RECEIVED")
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
						time.Sleep(time.Duration(client.VoiceHeartBeatInterval) * time.Millisecond)
					}
				}()
      case 9:
        log.Println("OP CODE 9 RECEIVED")
			}
		}
	}()
}

type speaking struct {
	Speaking int `json:"speaking"`
	Dealy    int `json:"delay"`
	Ssrc     int `json:"ssrc"`
}

func (client *DiscordClient) SendAudio(videoName []string) {
  dir, err := os.Getwd()
  if err != nil {
    log.Fatal(err)
  }
  fileName := strings.Join(videoName, " ")
  args := []string{"-o", dir + string(os.PathSeparator) + "music" + string(os.PathSeparator) + fileName + ".%(ext)s", "-q", "-x", "--audio-format", "opus", "ytsearch1:" + fileName}
  cmd := exec.Command("youtube-dl", args...)
  err = cmd.Run()
  if err != nil {
    log.Fatal(err)
  }

  js := payload{5, speaking{5, 0, 0}}
	client.muVoiceWrite.Lock()
  err = client.VoiceConn.WriteJSON(js)
  if err != nil {
    log.Fatal(err)
  }
	client.muVoiceWrite.Unlock()

  quit := make(chan bool)
  client.QuitAudioCh = quit
  quitAudio := make(chan bool)
  
  audio := client.soundEncoder(dir + string(os.PathSeparator) + "music" + string(os.PathSeparator) + fileName + ".opus", quit)
  go client.soundSender(audio, quitAudio, 960, SAMPLE_RATE, quit)
}


func (client *DiscordClient) soundEncoder(file string, quit <- chan bool) chan []byte {
  c := make(chan []byte) 
  
  go func() {
    pcmbuf := make([]int16, 16384)
    enc, err := opus.NewEncoder(48000, 2, opus.AppAudio)
    if err != nil {
      log.Fatal(err)
    }
    f, err := os.Open(file)
    if err != nil {
      log.Fatal(err)
    }
    s, err := opus.NewStream(f)
    if err != nil {
      log.Fatal(err)
    }
    defer f.Close()
    defer s.Close()
    defer close(c)
    for {
      n, err := s.Read(pcmbuf)
      if err == io.EOF {
        return
      } else if err != nil {
        log.Fatal(err)
      }
      if n == 960 {
        pcm := pcmbuf[:n * CHANNELS]
        data := make([]byte, 2000)
        n, err = enc.Encode(pcm, data)
        if err != nil {
          log.Fatal(err)
        }
        data = data[:n]
        select {
        case c <- data:
          //
        case <-quit:
          return
        }
      } else if n == 0 {
        return
      }
    }
  }()

  return c
}

func (client *DiscordClient) soundSender(audioChan <- chan []byte, quitAudio <-chan bool, frameSize, sampleRate int, quit <- chan bool) {
  sequence := client.Sequence
  timestamp := uint32(rand.Intn(530))

  header := make([]byte, 12)
  header[0] = 0x80
  header[1] = 0x78

  nonce := [24]byte{}

  timeIncr := uint32(frameSize / (sampleRate / 1000))

  ticker := time.NewTicker(time.Millisecond * time.Duration(timeIncr))

  var recvAudio []byte

  
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
	for {
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
		time.Sleep(time.Duration(milis) * time.Millisecond)

	}
}

func (client *DiscordClient) Close() {
	client.Conn.Close()
	client.VoiceConn.Close()
}
