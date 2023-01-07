package bot

import (
	"io"
	"log"
	"net/http"

	"github.com/xfrr/goffmpeg/transcoder"
)

func ReadAudioFromUrl(url string) io.Reader {
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

	buffer := make([]byte, 7000)
	go func() {
		defer func(w *io.PipeWriter) {
			err := w.Close()
			if err != nil {
				log.Println(err)
			}
		}(w)
		for {
			n, err := res.Body.Read(buffer)
			if err == io.EOF {
				return
			}
			if err != nil {
				log.Println(err)
				return
			}
			n, err = w.Write(buffer[:n])
			if err != nil {
				log.Println(err)
				return
			}
		}
	}()

	_ = trans.Run(false)

	return r
}
