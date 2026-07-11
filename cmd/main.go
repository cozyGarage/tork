package main

import (
	"fmt"
	"os"

	"github.com/cozyGarage/transformer/cli"
	"github.com/cozyGarage/transformer/conf"
)

func main() {
	if err := conf.LoadConfig(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	app := cli.New()

	if err := app.Run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
