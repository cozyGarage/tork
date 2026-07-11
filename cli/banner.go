package cli

import (
	"fmt"

	"github.com/cozyGarage/transformer"
	"github.com/cozyGarage/transformer/conf"
	"github.com/fatih/color"
	"github.com/rs/zerolog/log"
)

func displayBanner() {
	mode := conf.StringDefault("cli.banner.mode", "console")
	if mode == "off" {
		return
	}
	banner := color.WhiteString(fmt.Sprintf(`
  _____ ___ ____ ____ _____ ___ _____ ____ _____ 
 |_   _|_ _/ ___|  _ \_   _|_ _| ____|  _ \_   _|
   | |  | | |   | |_) || |  | ||  _| | |_) || |  
   | |  | | |___|  _ < | |  | || |___|  _ < | |  
   |_| |___\____|_| \_\|_| |___|_____|_| \_\|_|  

 Transformer — fork of Tork (github.com/runabol/tork)
 %s (%s)
`, tork.Version, tork.GitCommit))

	if mode == "console" {
		fmt.Println(banner)
	} else {
		log.Info().Msg(banner)
	}
}
