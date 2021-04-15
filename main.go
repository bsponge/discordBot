package main

import (
	"fmt"
	"net/http"
	"os"
  "log"
  "io/ioutil"
	"server.com/bot/discordClient"
)

func PrintMenu() {
	list := []string{"1.AUTH", "2.INIT", "3.CONNECT TO VOICE CHANNEL", "4.SEND VOICE", "5.QUIT"}
	for _, v := range list {
		fmt.Println(v)
	}
}

func codeEndpoint(w http.ResponseWriter, req *http.Request) {
	fmt.Println(req.URL.Query())
	authenticationCode, _ = req.URL.Query()["code"]
}

func endpoint(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Incoming request", *r)
}

var authenticationCode []string

func main() {
  content, err := ioutil.ReadFile("botToken.txt")
  if err != nil {
    log.Fatal(err)
  }
  botToken := string(content)
  content, err = ioutil.ReadFile("clientId.txt")
  if err != nil {
    log.Fatal(err)
  }
  clientId := string(content)
	client := discordClient.NewDiscordClient(clientId, botToken)
	go func() {
		for {
			PrintMenu()
			fmt.Print("Input: ")

			var i int
			fmt.Scanf("%d", &i)

			switch i {
			case 1:
				fmt.Println(client.GetAuthLink())
				http.HandleFunc("/", codeEndpoint)
				go http.ListenAndServe(":8080", nil)
			case 2:
				client.Gateway()
				//fmt.Println("2")
			case 3:
				client.RetrieveVoiceServerInformation()
				//fmt.Println("3")
			case 4:
				client.SendLambo()
				//fmt.Println("4")
			case 5:
				client.Close()
				fmt.Println("Bye!")
				os.Exit(0)
			}
		}
	}()
	for {
	}
}
