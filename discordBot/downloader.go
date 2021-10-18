package discordBot

import (
  "io"
  "net/http"
  "github.com/xfrr/goffmpeg/transcoder"
)

func ReadAudioFromUrl(url string) io.Reader {
  url = "https://r1---sn-f5f7kn7e.googlevideo.com/videoplayback?expire=1634575814&ei=ZlFtYYW-KZaF7QT2vbCoCA&ip=2a02%3Aa31a%3Aa03d%3A4e80%3A14c2%3A4fe2%3A5046%3A7e07&id=o-AEJvwB2I7IhltGkc0nDKSYUEToS7yEUS6yre4Bb5L0PQ&itag=250&source=youtube&requiressl=yes&mh=JL&mm=31%2C29&mn=sn-f5f7kn7e%2Csn-f5f7lnee&ms=au%2Crdu&mv=m&mvi=1&pl=46&initcwndbps=2020000&vprv=1&mime=audio%2Fwebm&ns=y7WzCIXncF2QAuanLiZsrmoG&gir=yes&clen=440012&dur=51.761&lmt=1612993733143847&mt=1634553899&fvip=1&keepalive=yes&fexp=24001373%2C24007246&c=WEB&txp=1432434&n=_wTDORzKuVy5sAAX8&sparams=expire%2Cei%2Cip%2Cid%2Citag%2Csource%2Crequiressl%2Cvprv%2Cmime%2Cns%2Cgir%2Cclen%2Cdur%2Clmt&sig=AOq0QJ8wRAIgBJ3jso7yCiHaBmfe0hGctn8A7Cixz5sIq6iTR7aMDS8CIAHeiLdbtiHjWhk2QHc5FAF7CgmK1X7b3smnLDEe422Z&lsparams=mh%2Cmm%2Cmn%2Cms%2Cmv%2Cmvi%2Cpl%2Cinitcwndbps&lsig=AG3C_xAwRAIgKkzi66BUjHTUN7GQu2AQ18II5002ZsixTbXuiRce6-MCIAWHVjEdsjPMKqzEwN0Abi0WjfzO7v0lOHx-stbhpT15"

  //args := []string{"-i", url, "-hide_banner", "-loglevel", "panic", "-acodec", "opus", "-f", "opus", "pipe:1"}
  trans := new(transcoder.Transcoder)
  err := trans.InitializeEmptyTranscoder()
  if err != nil {
    panic(err)
  }

  w, err := trans.CreateInputPipe()
  if err != nil {
    panic(err)
  }
  r, err := trans.CreateOutputPipe("opus")
  if err != nil {
    panic(err)
  }

  client := &http.Client{}
  req, err := http.NewRequest("GET", url, nil)
  if err != nil {
    panic(err)
  }

  res, err := client.Do(req)
  if err != nil {
    panic(err)
  }

  buffer := make([]byte, 3000)
  go func() {
    defer w.Close()
    for {
      n, err := res.Body.Read(buffer)
      if err == io.EOF {
        return
      }
      if err != nil {
        panic(err)
      }
      n, err = w.Write(buffer[:n])
      if err != nil {
        panic(err)
      }
    }
  }()

  _ = trans.Run(false)

  return r
}

