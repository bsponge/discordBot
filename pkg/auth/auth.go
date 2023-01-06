package auth

import (
	"context"
	"fmt"
	"net/http"
)

type Authenticator struct{}

func NewAuthenticator() (*Authenticator, error) {
	return &Authenticator{}, nil
}

func (a *Authenticator) Start(ctx context.Context) error {
	http.HandleFunc("/", codeEndpoint)
	go func() {
		err := http.ListenAndServe(":8080", nil)
		if err != nil {

		}
	}()

	return nil
}

func codeEndpoint(w http.ResponseWriter, req *http.Request) {
	fmt.Println(req.URL.Query())
	_, _ = req.URL.Query()["code"]
}
