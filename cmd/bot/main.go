package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/bsponge/discordBot/pkg/auth"
	"github.com/bsponge/discordBot/pkg/bot"
)

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-c
		log.Println("Main context canceled. Closing...")
		cancel()
	}()

	client, err := bot.NewDiscordClient()
	if err != nil {
		log.Fatal("Could not create client: ", err)
	}

	err = client.Start(ctx)
	if err != nil {
		log.Fatal("Could not start client: ", err)
	}

	authenticator, err := auth.NewAuthenticator()
	if err != nil {
		log.Fatal("Could not initialize authenticator: ", err)
	}

	err = authenticator.Start(ctx)
	if err != nil {
		log.Fatal("Could not start authenticator: ", err)
	}

	wait := make(chan struct{})

	select {
	case <-wait:
	case <-ctx.Done():
	}
}

func printMenu() {
	list := []string{"1.INIT", "2.QUIT"}
	for _, v := range list {
		fmt.Println(v)
	}
}
