package main

import (
	"fmt"
	"net/http"
	"os"
	"server.com/bot/discordClient"
)

var authenticationCode []string

func main() {
  //log.SetOutput(ioutil.Discard)

	client := discordClient.NewDiscordClient()

  go menu(&client)

  http.HandleFunc("/", codeEndpoint)
  go http.ListenAndServe(":8080", nil)

  	for {
		c := make(chan int)
		<-c
	}
}

func menu(client *discordClient.DiscordClient) {
  for {
    printMenu()
    fmt.Print("Input: ")

    var i int
    fmt.Scanf("%d", &i)

    switch i {
    case 1:
      client.CreateSocketConnection()
    case 2:
      client.RetrieveVoiceServerInformation()
    case 3:
      client.Close()
      fmt.Println("Bye!")
      os.Exit(0)
    }
  }
}

func printMenu() {
	list := []string{"1.INIT", "2.CONNECT TO VOICE CHANNEL", "3.QUIT"}
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




