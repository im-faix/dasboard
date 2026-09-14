package main

import (
	"log"

	"github.com/im-faix/sentinel/backend/internal/app"
)

func main() {

	application := app.New()

	if err := application.Run(); err != nil {
		log.Fatal(err)
	}
}
