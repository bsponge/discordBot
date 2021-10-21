package main

import (
	"fmt"
	"github.com/bsponge/discordBot/discordBot"
	"net/http"
	"os"
)

func main() {
	client := discordBot.NewDiscordClient()

	go menu(&client)

	http.HandleFunc("/", codeEndpoint)
	go func() {
		err := http.ListenAndServe(":8080", nil)
		if err != nil {

		}
	}()

	for {
		c := make(chan int)
		<-c
	}
}

func menu(client *discordBot.DiscordClient) {
	for {
		printMenu()
		fmt.Print("Input: ")

		var i int
		_, i2 := fmt.Scanf("%d", &i)
		if i2 != nil {
			return 
		}

		switch i {
		case 1:
			client.CreateSocketConnection()
		case 2:
			client.Close()
			fmt.Println("Bye!")
			os.Exit(0)
		}
	}
}

func printMenu() {
	list := []string{"1.INIT", "2.QUIT"}
	for _, v := range list {
		fmt.Println(v)
	}
}

func codeEndpoint(w http.ResponseWriter, req *http.Request) {
	fmt.Println(req.URL.Query())
	_, _ = req.URL.Query()["code"]
}

func endpoint(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Incoming request", *r)
}
